package com.foilen.box.android

import android.Manifest
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Intent
import android.content.IntentFilter
import android.content.pm.PackageManager
import android.database.Cursor
import android.net.Uri
import android.os.BatteryManager
import android.os.Build
import android.os.Bundle
import android.provider.Settings
import android.provider.Telephony
import android.telephony.SmsManager
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
import androidx.core.app.NotificationCompat
import androidx.core.content.ContextCompat
import mobile.BatteryProvider
import mobile.Mobile
import mobile.RealmStateSink
import mobile.SmsBridge
import org.json.JSONArray
import org.json.JSONObject

/**
 * Hosts the local web UI/API server (cmd/mobile.Mobile.startServer) in a
 * WebView. GPS uses the standard `navigator.geolocation` Web API (gps.js);
 * WebView supports it once onGeolocationPermissionsShowPrompt grants the
 * runtime permission, so no native location bridge is needed.
 *
 * Implements mobile.RealmStateSink (foreground notification on Realm
 * toggle), mobile.BatteryProvider (BatteryManager, since Go's sysfs
 * detection is SELinux-blocked on Android), and mobile.SmsBridge (SMS
 * send/import/notify; permissions requested on demand via
 * SmsPermissionBridge since most users never enable it).
 */
class MainActivity : ComponentActivity(), RealmStateSink, BatteryProvider, SmsBridge {

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
		webView.addJavascriptInterface(SmsPermissionBridge(this), "SmsPermissionBridge")
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

		// Required on Android 13+ for RealmForegroundService's foreground notification.
		if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU && !hasNotificationPermission()) {
			ActivityCompat.requestPermissions(
				this,
				arrayOf(Manifest.permission.POST_NOTIFICATIONS),
				NOTIFICATION_PERMISSION_REQUEST,
			)
		}

		startServerAndLoad()
		ContextCompat.startForegroundService(this, Intent(this, RealmForegroundService::class.java))
	}

	override fun onResume() {
		super.onResume()
		// WebView can go stale across a long backgrounding without a full onCreate
		// (e.g. server restarted on a new port); re-check and reload if so.
		Thread {
			try {
				val url = Mobile.startServer(filesDir.absolutePath, deviceName(), Build.VERSION.RELEASE, this, this, this)
				val currentUrl = webView.url
				if (currentUrl == null || !currentUrl.startsWith(url)) {
					runOnUiThread { webView.loadUrl("$url?platform=android") }
				}
			} catch (e: Exception) {
				Log.e(TAG, "failed to verify server on resume", e)
			}
		}.start()
	}

	// No onDestroy override: RealmForegroundService owns the engine and stops it
	// only when the user swipes the app away (RealmForegroundService.onTaskRemoved).

	private fun hasLocationPermission(): Boolean =
		ContextCompat.checkSelfPermission(this, Manifest.permission.ACCESS_FINE_LOCATION) ==
			PackageManager.PERMISSION_GRANTED

	private fun hasCameraPermission(): Boolean =
		ContextCompat.checkSelfPermission(this, Manifest.permission.CAMERA) == PackageManager.PERMISSION_GRANTED

	private fun startServerAndLoad() {
		// FLAG_ACTIVITY_CLEAR_TASK (see showNotification) forces a fresh onCreate,
		// so handling the deep link here covers both cold- and warm-start cases.
		val smsDeepLink = intent.getStringExtra(EXTRA_SMS_DEEP_LINK)
		Thread {
			try {
				val url = Mobile.startServer(filesDir.absolutePath, deviceName(), Build.VERSION.RELEASE, this, this, this)
				val target = if (smsDeepLink != null) {
					"$url?platform=android#realm/realm-sms-subtab/${Uri.encode(smsDeepLink)}"
				} else {
					"$url?platform=android"
				}
				runOnUiThread { webView.loadUrl(target) }
			} catch (e: Exception) {
				Log.e(TAG, "failed to start server", e)
			}
		}.start()
	}

	// Reported as the peer's "hostname" since Go's os.Hostname() always
	// returns "localhost" on Android.
	private fun deviceName(): String =
		Settings.Global.getString(contentResolver, Settings.Global.DEVICE_NAME) ?: Build.MODEL

	private fun hasNotificationPermission(): Boolean =
		ContextCompat.checkSelfPermission(this, Manifest.permission.POST_NOTIFICATIONS) ==
			PackageManager.PERMISSION_GRANTED

	// Called from Go on realm.setEnabled; forwarded so the foreground
	// notification can be dropped/restored accordingly.
	override fun setRealmEnabled(enabled: Boolean) {
		RealmForegroundService.setRealmEnabled(this, enabled)
	}

	// mobile.BatteryProvider: called synchronously from Go when the specs report is generated.
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

	// mobile.SmsBridge: called from internal/sms.Manager when a create-request targets this device.
	override fun sendSms(phoneNumber: String, body: String) {
		val smsManager = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) {
			getSystemService(SmsManager::class.java)
		} else {
			@Suppress("DEPRECATION")
			SmsManager.getDefault()
		}
		smsManager.sendTextMessage(phoneNumber, null, body, null, null)
	}

	// mobile.SmsBridge: bulk-imports full SMS history when SMS management is
	// first enabled. Reading content://sms only needs READ_SMS, not being the
	// default SMS app. Includes a "raw" full-row dump (dumpRow) to hunt for an
	// undocumented column reflecting the SMS app's "Trash" state.
	override fun readAllSms(): String {
		val result = JSONArray()
		contentResolver.query(Telephony.Sms.CONTENT_URI, null, null, null, "${Telephony.Sms.DATE} ASC")
			?.use { cursor ->
				val addressIdx = cursor.getColumnIndexOrThrow(Telephony.Sms.ADDRESS)
				val bodyIdx = cursor.getColumnIndexOrThrow(Telephony.Sms.BODY)
				val dateIdx = cursor.getColumnIndexOrThrow(Telephony.Sms.DATE)
				val typeIdx = cursor.getColumnIndexOrThrow(Telephony.Sms.TYPE)
				while (cursor.moveToNext()) {
					val address = cursor.getString(addressIdx) ?: continue
					val body = cursor.getString(bodyIdx) ?: ""
					val date = cursor.getLong(dateIdx)
					val outgoing = cursor.getInt(typeIdx) == Telephony.Sms.MESSAGE_TYPE_SENT
					result.put(
						JSONObject().apply {
							put("phoneNumber", address)
							put("direction", if (outgoing) "outgoing" else "incoming")
							put("body", body)
							put("sender", if (outgoing) "" else address)
							put("receiver", if (outgoing) address else "")
							put("timestampUnixMillis", date)
							put("raw", dumpRow(cursor))
						},
					)
				}
			}
		return result.toString()
	}

	// Reads every column of cursor's current row into a JSONObject; BLOB
	// columns are summarized by length instead of dumped as text.
	private fun dumpRow(cursor: Cursor): JSONObject {
		val raw = JSONObject()
		for (i in 0 until cursor.columnCount) {
			val value: Any = when (cursor.getType(i)) {
				Cursor.FIELD_TYPE_NULL -> JSONObject.NULL
				Cursor.FIELD_TYPE_BLOB -> "<blob:${cursor.getBlob(i)?.size ?: 0} bytes>"
				else -> cursor.getString(i) ?: JSONObject.NULL
			}
			raw.put(cursor.getColumnName(i), value)
		}
		return raw
	}

	// mobile.SmsBridge: alerts on a new message in an SMS-* store this device
	// doesn't own. Clicking reopens MainActivity with deepLink
	// ("groupId|storeName|phoneNumber") into the SMS subtab (startServerAndLoad).
	override fun showNotification(title: String, body: String, deepLink: String) {
		val channel = NotificationChannel(
			SMS_NOTIFICATION_CHANNEL_ID,
			"SMS messages",
			NotificationManager.IMPORTANCE_HIGH,
		).apply { description = "New SMS messages synced through Foilen Box" }
		getSystemService(NotificationManager::class.java).createNotificationChannel(channel)

		val contentIntent = PendingIntent.getActivity(
			this,
			deepLink.hashCode(),
			Intent(this, MainActivity::class.java)
				.putExtra(EXTRA_SMS_DEEP_LINK, deepLink)
				.setFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TASK),
			PendingIntent.FLAG_IMMUTABLE,
		)

		val notification = NotificationCompat.Builder(this, SMS_NOTIFICATION_CHANNEL_ID)
			.setSmallIcon(android.R.drawable.ic_dialog_email)
			.setContentTitle(title)
			.setContentText(body)
			.setPriority(NotificationCompat.PRIORITY_HIGH)
			.setAutoCancel(true)
			.setContentIntent(contentIntent)
			.build()
		getSystemService(NotificationManager::class.java).notify(deepLink.hashCode(), notification)
	}

	companion object {
		private const val TAG = "FoilenBox"
		private const val LOCATION_PERMISSION_REQUEST = 1
		private const val CAMERA_PERMISSION_REQUEST = 2
		private const val NOTIFICATION_PERMISSION_REQUEST = 3
		private const val SMS_NOTIFICATION_CHANNEL_ID = "sms_messages"
		private const val EXTRA_SMS_DEEP_LINK = "smsDeepLink"
	}
}
