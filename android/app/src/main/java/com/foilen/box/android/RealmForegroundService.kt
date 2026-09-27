package com.foilen.box.android

import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.Service
import android.content.Context
import android.app.PendingIntent
import android.content.Intent
import android.content.pm.ServiceInfo
import android.net.wifi.WifiManager
import android.os.Build
import android.os.Handler
import android.os.IBinder
import android.os.Looper
import android.util.Log
import androidx.core.app.NotificationCompat
import androidx.core.app.ServiceCompat
import mobile.Mobile

class RealmForegroundService : Service() {

	private var multicastLock: WifiManager.MulticastLock? = null

	private val handler = Handler(Looper.getMainLooper())

	private var dutyCycleTick = 0

	private val peerCountRefresher = object : Runnable {
		override fun run() {
			updateNotification()
			refreshMulticastLockDutyCycle()
			handler.postDelayed(this, PEER_COUNT_REFRESH_MS)
		}
	}

	private fun refreshMulticastLockDutyCycle() {
		dutyCycleTick = (dutyCycleTick + 1) % DUTY_CYCLE_TICKS
		if (dutyCycleTick == 0) {
			acquireMulticastLock()
		} else {
			releaseMulticastLock()
		}
	}

	override fun onCreate() {
		super.onCreate()
		isRunning = true
		createNotificationChannel()
		showNotification()

		val realmEnabled = AndroidConfigPrefs.isRealmEnabled(this)
		AndroidConfigPrefs.setServiceExpected(this, realmEnabled)
		if (realmEnabled) {
			ServiceWatchdog.schedule(this)
			handler.post(peerCountRefresher)
			acquireMulticastLock()
		} else {
			ServiceWatchdog.cancel(this)
			stopForeground(STOP_FOREGROUND_REMOVE)
		}
		Thread {
			try {
				Mobile.startServer(
					filesDir.absolutePath,
					deviceName(this),
					Build.VERSION.RELEASE,
					null,
					null,
					null,
					CameraCaptureBridge(this),
				)
			} catch (e: Exception) {
				Log.e(TAG, "failed to start server", e)
			}
		}.start()
	}

	private fun acquireMulticastLock() {
		if (multicastLock?.isHeld == true) return
		val wifiManager = applicationContext.getSystemService(Context.WIFI_SERVICE) as WifiManager
		val lock = wifiManager.createMulticastLock(TAG)
		lock.setReferenceCounted(false)
		lock.acquire()
		multicastLock = lock
	}

	private fun releaseMulticastLock() {
		multicastLock?.let { if (it.isHeld) it.release() }
		multicastLock = null
	}

	override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
		if (intent?.hasExtra(EXTRA_ENABLED) == true) {
			if (intent.getBooleanExtra(EXTRA_ENABLED, true)) {
				AndroidConfigPrefs.setRealmEnabled(this, true)
				AndroidConfigPrefs.setServiceExpected(this, true)
				ServiceWatchdog.schedule(this)
				acquireMulticastLock()
				showNotification()
				handler.removeCallbacks(peerCountRefresher)
				handler.postDelayed(peerCountRefresher, PEER_COUNT_REFRESH_MS)
			} else {
				AndroidConfigPrefs.setRealmEnabled(this, false)
				AndroidConfigPrefs.setServiceExpected(this, false)
				ServiceWatchdog.cancel(this)
				handler.removeCallbacks(peerCountRefresher)
				releaseMulticastLock()
				stopForeground(STOP_FOREGROUND_REMOVE)
			}
		}
		return START_STICKY
	}

	private fun showNotification() {
		if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
			ServiceCompat.startForeground(
				this,
				NOTIFICATION_ID,
				buildNotification(),
				ServiceInfo.FOREGROUND_SERVICE_TYPE_CONNECTED_DEVICE,
			)
		} else {
			startForeground(NOTIFICATION_ID, buildNotification())
		}
	}

	private fun updateNotification() {
		getSystemService(NotificationManager::class.java).notify(NOTIFICATION_ID, buildNotification())
	}

	private fun buildNotification(): android.app.Notification {
		val contentIntent = PendingIntent.getActivity(
			this,
			0,
			Intent(this, MainActivity::class.java).setFlags(
				Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TASK,
			),
			PendingIntent.FLAG_IMMUTABLE,
		)

		val connected = Mobile.connectedPeersCount()
		val total = Mobile.peersTotalCount()

		return NotificationCompat.Builder(this, NOTIFICATION_CHANNEL_ID)
			.setSmallIcon(android.R.drawable.ic_dialog_info)
			.setContentTitle("Foilen Box")
			.setContentText("Connected peers $connected/$total")
			.setPriority(NotificationCompat.PRIORITY_LOW)
			.setOngoing(true)
			.setContentIntent(contentIntent)
			.build()
	}

	override fun onTimeout(startId: Int, fgsType: Int) {
		stopForeground(STOP_FOREGROUND_REMOVE)
		stopSelf(startId)
	}

	override fun onBind(intent: Intent?): IBinder? = null

	override fun onTaskRemoved(rootIntent: Intent?) {
		if (AndroidConfigPrefs.isServiceExpected(this)) {
			ServiceWatchdog.schedule(this)
		}
		super.onTaskRemoved(rootIntent)
	}

	override fun onDestroy() {
		isRunning = false
		handler.removeCallbacks(peerCountRefresher)
		releaseMulticastLock()
		super.onDestroy()
	}

	private fun createNotificationChannel() {
		val channel = NotificationChannel(
			NOTIFICATION_CHANNEL_ID,
			"Background connection",
			NotificationManager.IMPORTANCE_LOW,
		).apply {
			description = "Keeps realm peer connections alive while the app is in the background"
		}
		getSystemService(NotificationManager::class.java).createNotificationChannel(channel)
	}

	companion object {
		private const val TAG = "FoilenBox"

		@Volatile
		var isRunning = false
			private set
		private const val NOTIFICATION_CHANNEL_ID = "realm_peer_service"
		private const val NOTIFICATION_ID = 2
		private const val EXTRA_ENABLED = "enabled"
		private const val PEER_COUNT_REFRESH_MS = 5 * 60_000L

		private const val DUTY_CYCLE_TICKS = 6

		fun setRealmEnabled(context: Context, enabled: Boolean) {
			val intent = Intent(context, RealmForegroundService::class.java)
				.putExtra(EXTRA_ENABLED, enabled)
			context.startService(intent)
		}
	}
}
