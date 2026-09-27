package com.foilen.box.android

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import androidx.core.content.ContextCompat

class BootCompletedReceiver : BroadcastReceiver() {

	override fun onReceive(context: Context, intent: Intent) {
		if (intent.action != Intent.ACTION_BOOT_COMPLETED) return
		if (!AndroidConfigPrefs.isBootAutostartEnabled(context)) return

		ContextCompat.startForegroundService(context, Intent(context, RealmForegroundService::class.java))
		ServiceWatchdog.schedule(context)
	}
}
