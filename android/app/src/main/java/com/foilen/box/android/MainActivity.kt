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
import android.os.PowerManager
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

class MainActivity : ComponentActivity(), RealmStateSink, BatteryProvider, SmsBridge {

	private lateinit var webView: WebView
	private var filePickerCallback: ValueCallback<Array<Uri>>? = null
	private lateinit var filePickerLauncher: ActivityResultLauncher<Intent>
	private lateinit var cameraCaptureBridge: CameraCaptureBridge

	override fun onCreate(savedInstanceState: Bundle?) {
		super.onCreate(savedInstanceState)

		cameraCaptureBridge = CameraCaptureBridge(this)

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
		webView.settings.cacheMode = android.webkit.WebSettings.LOAD_NO_CACHE
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

		val cameraPerms = arrayOf(Manifest.permission.CAMERA, Manifest.permission.RECORD_AUDIO)
			.filter { ContextCompat.checkSelfPermission(this, it) != PackageManager.PERMISSION_GRANTED }
		if (cameraPerms.isNotEmpty()) {
			ActivityCompat.requestPermissions(this, cameraPerms.toTypedArray(), CAMERA_PERMISSION_REQUEST)
		}

		if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU && !hasNotificationPermission()) {
			ActivityCompat.requestPermissions(
				this,
				arrayOf(Manifest.permission.POST_NOTIFICATIONS),
				NOTIFICATION_PERMISSION_REQUEST,
			)
		}

		requestIgnoreBatteryOptimizations()

		startServerAndLoad()
		ContextCompat.startForegroundService(this, Intent(this, RealmForegroundService::class.java))
	}

	override fun onResume() {
		super.onResume()
		Thread {
			try {
				val url = startServer()
				runOnUiThread {
					val currentUrl = webView.url
					if (currentUrl == null || !currentUrl.startsWith(url)) {
						webView.loadUrl("$url?platform=android")
					}
				}
			} catch (e: Exception) {
				Log.e(TAG, "failed to verify server on resume", e)
			}
		}.start()
	}

	private fun requestIgnoreBatteryOptimizations() {
		val powerManager = getSystemService(PowerManager::class.java) ?: return
		if (powerManager.isIgnoringBatteryOptimizations(packageName)) return
		try {
			startActivity(
				Intent(Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS)
					.setData(Uri.parse("package:$packageName")),
			)
		} catch (e: Exception) {
			Log.w(TAG, "failed to request battery optimization exemption", e)
		}
	}

	private fun hasLocationPermission(): Boolean =
		ContextCompat.checkSelfPermission(this, Manifest.permission.ACCESS_FINE_LOCATION) ==
			PackageManager.PERMISSION_GRANTED

	private fun hasCameraPermission(): Boolean =
		ContextCompat.checkSelfPermission(this, Manifest.permission.CAMERA) == PackageManager.PERMISSION_GRANTED

	private fun startServerAndLoad() {
		val smsDeepLink = intent.getStringExtra(EXTRA_SMS_DEEP_LINK)
		Thread {
			try {
				val url = startServer()
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

	private fun startServer(): String =
		Mobile.startServer(filesDir.absolutePath, deviceName(this), Build.VERSION.RELEASE, this, this, this, cameraCaptureBridge)

	private fun hasNotificationPermission(): Boolean =
		ContextCompat.checkSelfPermission(this, Manifest.permission.POST_NOTIFICATIONS) ==
			PackageManager.PERMISSION_GRANTED

	// mobile.RealmStateSink

	override fun setRealmEnabled(enabled: Boolean) {
		RealmForegroundService.setRealmEnabled(this, enabled)
	}

	// mobile.BatteryProvider

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

	// mobile.SmsBridge

	override fun sendSms(phoneNumber: String, body: String) {
		val smsManager = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) {
			getSystemService(SmsManager::class.java)
		} else {
			@Suppress("DEPRECATION")
			SmsManager.getDefault()
		}
		smsManager.sendTextMessage(phoneNumber, null, body, null, null)
	}

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
		readAllMmsInto(result)
		return result.toString()
	}

	private fun readAllMmsInto(result: JSONArray) {
		contentResolver.query(Telephony.Mms.CONTENT_URI, null, null, null, "${Telephony.Mms.DATE} ASC")
			?.use { cursor ->
				val idIdx = cursor.getColumnIndexOrThrow(Telephony.Mms._ID)
				val dateIdx = cursor.getColumnIndexOrThrow(Telephony.Mms.DATE)
				val boxIdx = cursor.getColumnIndexOrThrow(Telephony.Mms.MESSAGE_BOX)
				while (cursor.moveToNext()) {
					val id = cursor.getLong(idIdx)
					val outgoing = cursor.getInt(boxIdx) != Telephony.Mms.MESSAGE_BOX_INBOX
					val address = mmsAddress(id, outgoing) ?: continue
					val timestampMillis = cursor.getLong(dateIdx) * 1000
					result.put(
						JSONObject().apply {
							put("phoneNumber", address)
							put("direction", if (outgoing) "outgoing" else "incoming")
							put("body", mmsBody(id))
							put("sender", if (outgoing) "" else address)
							put("receiver", if (outgoing) address else "")
							put("timestampUnixMillis", timestampMillis)
							put("raw", dumpRow(cursor))
						},
					)
				}
			}
	}

	private val mmsAddrTypeFrom = 137
	private val mmsAddrTypeTo = 151

	private fun mmsAddress(mmsId: Long, outgoing: Boolean): String? {
		val wantType = if (outgoing) mmsAddrTypeTo else mmsAddrTypeFrom
		contentResolver.query(Uri.parse("content://mms/$mmsId/addr"), null, null, null, null)?.use { cursor ->
			val addressIdx = cursor.getColumnIndexOrThrow("address")
			val typeIdx = cursor.getColumnIndexOrThrow("type")
			while (cursor.moveToNext()) {
				if (cursor.getInt(typeIdx) != wantType) continue
				val address = cursor.getString(addressIdx)
				if (address != null && address != "insert-address-token") return address
			}
		}
		return null
	}

	private fun mmsBody(mmsId: Long): String {
		val textParts = mutableListOf<String>()
		var hasMedia = false
		contentResolver.query(Uri.parse("content://mms/$mmsId/part"), null, null, null, null)?.use { cursor ->
			val ctIdx = cursor.getColumnIndexOrThrow("ct")
			val idIdx = cursor.getColumnIndexOrThrow("_id")
			val textColIdx = cursor.getColumnIndex("text")
			while (cursor.moveToNext()) {
				when (val contentType = cursor.getString(ctIdx)) {
					null, "application/smil" -> {}
					"text/plain" -> {
						val inlineText = if (textColIdx >= 0) cursor.getString(textColIdx) else null
						textParts.add(inlineText ?: readMmsPartText(cursor.getLong(idIdx)) ?: "")
					}
					else -> hasMedia = true
				}
			}
		}
		val text = textParts.joinToString("\n").trim()
		return when {
			text.isNotEmpty() && hasMedia -> "$text (media)"
			text.isNotEmpty() -> text
			hasMedia -> "(media)"
			else -> ""
		}
	}

	private fun readMmsPartText(partId: Long): String? =
		try {
			contentResolver.openInputStream(Uri.parse("content://mms/part/$partId"))?.use { it.readBytes().toString(Charsets.UTF_8) }
		} catch (e: Exception) {
			null
		}

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
