package com.foilen.box.android

import android.Manifest
import android.content.Intent
import android.content.IntentFilter
import android.content.pm.PackageManager
import android.net.Uri
import android.os.BatteryManager
import android.os.Build
import android.os.Bundle
import android.provider.Settings
import android.util.Log
import android.webkit.GeolocationPermissions
import android.webkit.PermissionRequest
import android.webkit.ValueCallback
import android.webkit.WebChromeClient
import android.webkit.WebView
import androidx.activity.ComponentActivity
import androidx.activity.OnBackPressedCallback
import androidx.activity.result.ActivityResult
import androidx.activity.result.ActivityResultLauncher
import androidx.activity.result.contract.ActivityResultContracts
import androidx.core.app.ActivityCompat
import androidx.core.content.ContextCompat
import mobile.BatteryProvider
import mobile.Mobile
import mobile.RealmStateSink

/**
 * Hosts the same local web UI/API server the desktop build runs
 * (internal/webserver, started here through the gomobile-bound
 * cmd/mobile.Mobile.startServer) in a WebView. Real-time GPS is provided by
 * the standard `navigator.geolocation` Web API in web/js/gps.js — WebView
 * supports it once the Android runtime permission is granted and
 * onGeolocationPermissionsShowPrompt answers the JS-side request, so no
 * native location bridge/JNI code is needed on this side.
 *
 * Also implements mobile.RealmStateSink so RealmForegroundService's
 * notification can be dropped (and restored) when the user toggles Realm
 * networking off/on from the web UI's Realm settings.
 *
 * Also implements mobile.BatteryProvider so the Specs tab can show battery
 * info: Go's usual sysfs-based detection (internal/spec) can't read
 * /sys/class/power_supply on Android, since that's commonly blocked by
 * SELinux for regular (non-system) apps, so BatteryManager is used instead.
 */
class MainActivity : ComponentActivity(), RealmStateSink, BatteryProvider {

	private lateinit var webView: WebView
	private var filePickerCallback: ValueCallback<Array<Uri>>? = null
	private lateinit var filePickerLauncher: ActivityResultLauncher<Intent>

	override fun onCreate(savedInstanceState: Bundle?) {
		super.onCreate(savedInstanceState)

		filePickerLauncher =
			registerForActivityResult(ActivityResultContracts.StartActivityForResult()) { result: ActivityResult ->
				val callback = filePickerCallback
				filePickerCallback = null
				callback?.onReceiveValue(WebChromeClient.FileChooserParams.parseResult(result.resultCode, result.data))
			}

		webView = WebView(this)
		setContentView(webView)

		webView.settings.javaScriptEnabled = true
		webView.settings.setGeolocationEnabled(true)
		webView.addJavascriptInterface(AndroidConfigBridge(this), "AndroidConfigBridge")
		webView.webChromeClient = object : WebChromeClient() {
			override fun onGeolocationPermissionsShowPrompt(
				origin: String,
				callback: GeolocationPermissions.Callback,
			) {
				callback.invoke(origin, hasLocationPermission(), false)
			}

			override fun onPermissionRequest(request: PermissionRequest) {
				val resources = request.resources.filter { it == PermissionRequest.RESOURCE_VIDEO_CAPTURE }
				if (resources.isNotEmpty() && hasCameraPermission()) {
					request.grant(resources.toTypedArray())
				} else {
					request.deny()
				}
			}

			override fun onShowFileChooser(
				webView: WebView,
				filePathCallback: ValueCallback<Array<Uri>>,
				fileChooserParams: FileChooserParams,
			): Boolean {
				filePickerCallback?.onReceiveValue(null)
				filePickerCallback = filePathCallback
				filePickerLauncher.launch(fileChooserParams.createIntent())
				return true
			}
		}

		onBackPressedDispatcher.addCallback(
			this,
			object : OnBackPressedCallback(true) {
				override fun handleOnBackPressed() {
					if (webView.canGoBack()) webView.goBack() else finish()
				}
			},
		)

		if (!hasLocationPermission()) {
			ActivityCompat.requestPermissions(
				this,
				arrayOf(Manifest.permission.ACCESS_FINE_LOCATION, Manifest.permission.ACCESS_COARSE_LOCATION),
				LOCATION_PERMISSION_REQUEST,
			)
		}

		if (!hasCameraPermission()) {
			ActivityCompat.requestPermissions(this, arrayOf(Manifest.permission.CAMERA), CAMERA_PERMISSION_REQUEST)
		}

		// Needed for RealmForegroundService's "keeping connections alive"
		// notification, required on Android 13+ to run a foreground service.
		if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU && !hasNotificationPermission()) {
			ActivityCompat.requestPermissions(
				this,
				arrayOf(Manifest.permission.POST_NOTIFICATIONS),
				NOTIFICATION_PERMISSION_REQUEST,
			)
		}

		startServerAndLoad()
		// Keeps the engine's peer connections alive while the app is
		// backgrounded (app switch, screen off, ...) — see class doc there.
		ContextCompat.startForegroundService(this, Intent(this, RealmForegroundService::class.java))
	}

	override fun onResume() {
		super.onResume()
		// The activity can survive being backgrounded for a while without a full
		// onCreate, but the WebView's socket/session can still go stale (renderer
		// throttling, the server having restarted on a new port, ...). Re-check
		// the server URL and reload if it no longer matches what's loaded.
		Thread {
			try {
				val url = Mobile.startServer(filesDir.absolutePath, deviceName(), this, this)
				val currentUrl = webView.url
				if (currentUrl == null || !currentUrl.startsWith(url)) {
					runOnUiThread { webView.loadUrl("$url?platform=android") }
				}
			} catch (e: Exception) {
				Log.e(TAG, "failed to verify server on resume", e)
			}
		}.start()
	}

	// No onDestroy override: the engine is owned by RealmForegroundService
	// now, so it survives ordinary Activity destruction (backgrounding
	// under memory pressure, config changes) and is only stopped when the
	// user swipes the app away from recents (see
	// RealmForegroundService.onTaskRemoved).

	private fun hasLocationPermission(): Boolean =
		ContextCompat.checkSelfPermission(this, Manifest.permission.ACCESS_FINE_LOCATION) ==
			PackageManager.PERMISSION_GRANTED

	private fun hasCameraPermission(): Boolean =
		ContextCompat.checkSelfPermission(this, Manifest.permission.CAMERA) == PackageManager.PERMISSION_GRANTED

	private fun startServerAndLoad() {
		Thread {
			try {
				val url = Mobile.startServer(filesDir.absolutePath, deviceName(), this, this)
				runOnUiThread { webView.loadUrl("$url?platform=android") }
			} catch (e: Exception) {
				Log.e(TAG, "failed to start server", e)
			}
		}.start()
	}

	// The Realm engine reports this as the peer's "hostname"; Go's
	// os.Hostname() always returns "localhost" on Android, so use the
	// user-visible device name (Settings > About phone > Device name)
	// instead, falling back to the model if it's unset.
	private fun deviceName(): String =
		Settings.Global.getString(contentResolver, Settings.Global.DEVICE_NAME) ?: Build.MODEL

	private fun hasNotificationPermission(): Boolean =
		ContextCompat.checkSelfPermission(this, Manifest.permission.POST_NOTIFICATIONS) ==
			PackageManager.PERMISSION_GRANTED

	// Called from a background Go goroutine whenever the user toggles Realm
	// networking on/off (realm.setEnabled). Forwarded to
	// RealmForegroundService so it can drop its "keeping connections alive"
	// notification while there's nothing to keep alive, and restore it if
	// re-enabled.
	override fun setRealmEnabled(enabled: Boolean) {
		RealmForegroundService.setRealmEnabled(this, enabled)
	}

	// mobile.BatteryProvider: called synchronously from Go whenever the specs
	// report is (re)generated (at most once a day, see realm_announce.go, or
	// on-demand from the Specs tab).
	override fun batteryPercent(): Int {
		val bm = getSystemService(BatteryManager::class.java) ?: return -1
		val percent = bm.getIntProperty(BatteryManager.BATTERY_PROPERTY_CAPACITY)
		return if (percent in 0..100) percent else -1
	}

	override fun batteryStatus(): String {
		val status = registerReceiver(null, IntentFilter(Intent.ACTION_BATTERY_CHANGED))
			?.getIntExtra(BatteryManager.EXTRA_STATUS, -1)
		return when (status) {
			BatteryManager.BATTERY_STATUS_CHARGING -> "Charging"
			BatteryManager.BATTERY_STATUS_DISCHARGING -> "Discharging"
			BatteryManager.BATTERY_STATUS_FULL -> "Full"
			BatteryManager.BATTERY_STATUS_NOT_CHARGING -> "Not Charging"
			else -> ""
		}
	}

	companion object {
		private const val TAG = "FoilenBox"
		private const val LOCATION_PERMISSION_REQUEST = 1
		private const val CAMERA_PERMISSION_REQUEST = 2
		private const val NOTIFICATION_PERMISSION_REQUEST = 3
	}
}
