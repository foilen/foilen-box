package com.foilen.box.android

import android.app.AlarmManager
import android.app.PendingIntent
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.os.SystemClock
import android.util.Log
import androidx.core.content.ContextCompat

/**
 * Backstop for RealmForegroundService. START_STICKY alone doesn't reliably
 * bring the service back after the OS SIGKILLs it under memory pressure or
 * Doze, so an inexact repeating alarm re-checks every ~15 min and restarts
 * the service whenever it's expected to run
 * (AndroidConfigPrefs.isServiceExpected) but isn't
 * (RealmForegroundService.isRunning).
 */
object ServiceWatchdog {

	private const val REQUEST_CODE = 42

	fun schedule(context: Context) {
		val alarmManager = context.getSystemService(AlarmManager::class.java) ?: return
		alarmManager.setInexactRepeating(
			AlarmManager.ELAPSED_REALTIME_WAKEUP,
			SystemClock.elapsedRealtime() + AlarmManager.INTERVAL_FIFTEEN_MINUTES,
			AlarmManager.INTERVAL_FIFTEEN_MINUTES,
			pendingIntent(context),
		)
	}

	fun cancel(context: Context) {
		context.getSystemService(AlarmManager::class.java)?.cancel(pendingIntent(context))
	}

	private fun pendingIntent(context: Context): PendingIntent =
		PendingIntent.getBroadcast(
			context,
			REQUEST_CODE,
			Intent(context, WatchdogReceiver::class.java),
			PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
		)
}

class WatchdogReceiver : BroadcastReceiver() {
	override fun onReceive(context: Context, intent: Intent) {
		if (!AndroidConfigPrefs.isServiceExpected(context)) {
			ServiceWatchdog.cancel(context)
			return
		}
		if (RealmForegroundService.isRunning) return
		Log.w("FoilenBox", "watchdog: RealmForegroundService is down, restarting it")
		ContextCompat.startForegroundService(context, Intent(context, RealmForegroundService::class.java))
	}
}
