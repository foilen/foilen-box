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
			Log.w("FoilenBox", "watchdog: failed to restart service", e)
			return Result.retry()
		}
		return Result.success()
	}
}
