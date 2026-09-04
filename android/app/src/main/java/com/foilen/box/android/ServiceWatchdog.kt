package com.foilen.box.android

import android.content.Context
import android.content.Intent
import android.util.Log
import androidx.core.content.ContextCompat
import androidx.work.Constraints
import androidx.work.ExistingPeriodicWorkPolicy
import androidx.work.PeriodicWorkRequestBuilder
import androidx.work.WorkManager
import androidx.work.Worker
import androidx.work.WorkerParameters
import java.util.concurrent.TimeUnit

/**
 * Backstop for RealmForegroundService. START_STICKY alone doesn't reliably
 * bring the service back after the OS SIGKILLs it under memory pressure or
 * Doze, and the previous AlarmManager setInexactRepeating alarm was worse:
 * in practice it stopped firing entirely once the process dropped to
 * cached-empty (Doze defers inexact alarms indefinitely and the app
 * freezer withholds delivery from a frozen process).
 *
 * WorkManager periodic work runs in a fresh process (which unfreezes the
 * app), persists across reboots on its own, and fires during Doze
 * maintenance windows. Every ~15 min it restarts the service whenever it's
 * expected to run (AndroidConfigPrefs.isServiceExpected) but isn't
 * (RealmForegroundService.isRunning).
 */
object ServiceWatchdog {

	private const val WORK_NAME = "realm-foreground-service-watchdog"

	fun schedule(context: Context) {
		val request = PeriodicWorkRequestBuilder<WatchdogWorker>(15, TimeUnit.MINUTES)
			.setConstraints(Constraints.NONE)
			.build()
		WorkManager.getInstance(context).enqueueUniquePeriodicWork(
			WORK_NAME,
			ExistingPeriodicWorkPolicy.KEEP,
			request,
		)
	}

	fun cancel(context: Context) {
		WorkManager.getInstance(context).cancelUniqueWork(WORK_NAME)
	}
}

class WatchdogWorker(context: Context, params: WorkerParameters) : Worker(context, params) {
	override fun doWork(): Result {
		val context = applicationContext
		if (!AndroidConfigPrefs.isServiceExpected(context)) {
			ServiceWatchdog.cancel(context)
			return Result.success()
		}
		if (RealmForegroundService.isRunning) return Result.success()

		Log.w("FoilenBox", "watchdog: RealmForegroundService is down, restarting it")
		try {
			ContextCompat.startForegroundService(context, Intent(context, RealmForegroundService::class.java))
		} catch (e: Exception) {
			// Background FGS-start is normally allowed here (the app is
			// battery-optimization exempt); retry on the next period if not.
			Log.w("FoilenBox", "watchdog: failed to restart service", e)
			return Result.retry()
		}
		return Result.success()
	}
}
