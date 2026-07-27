package com.foilen.box.android

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import androidx.core.content.ContextCompat

/**
 * Starts RealmForegroundService right after the device finishes booting, if
 * the user opted into it from the web UI's Android tab (see
 * AndroidConfigPrefs). This lets the realm engine keep syncing without the
 * user having to open the app first.
 *
 * Android grants an exemption to start a foreground service from a
 * BOOT_COMPLETED receiver even though the app isn't otherwise in the
 * foreground, so no extra permission beyond RECEIVE_BOOT_COMPLETED is
 * needed.
 */
class BootCompletedReceiver : BroadcastReceiver() {

	override fun onReceive(context: Context, intent: Intent) {
		if (intent.action != Intent.ACTION_BOOT_COMPLETED) return
		if (!AndroidConfigPrefs.isBootAutostartEnabled(context)) return

		ContextCompat.startForegroundService(context, Intent(context, RealmForegroundService::class.java))
	}
}
