package com.foilen.box.android

import android.Manifest
import android.content.pm.PackageManager
import android.webkit.JavascriptInterface
import androidx.core.app.ActivityCompat
import androidx.core.content.ContextCompat

/**
 * Exposed to the WebView as `window.SmsPermissionBridge` (see
 * MainActivity.onCreate) so the web UI's SMS config (js/realm-sms.js) can
 * check/request READ_SMS/SEND_SMS/RECEIVE_SMS on demand — unlike
 * location/camera, these are only requested when the user actually enables
 * SMS management, not unconditionally at startup, since most users will
 * never turn this on.
 */
class SmsPermissionBridge(private val activity: MainActivity) {

	@JavascriptInterface
	fun hasPermission(): Boolean = PERMISSIONS.all {
		ContextCompat.checkSelfPermission(activity, it) == PackageManager.PERMISSION_GRANTED
	}

	@JavascriptInterface
	fun requestPermission() {
		activity.runOnUiThread {
			ActivityCompat.requestPermissions(activity, PERMISSIONS, SMS_PERMISSION_REQUEST)
		}
	}

	companion object {
		private val PERMISSIONS = arrayOf(
			Manifest.permission.READ_SMS,
			Manifest.permission.SEND_SMS,
			Manifest.permission.RECEIVE_SMS,
		)
		const val SMS_PERMISSION_REQUEST = 4
	}
}
