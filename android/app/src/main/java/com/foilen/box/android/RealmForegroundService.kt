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
import android.os.IBinder
import android.provider.Settings
import android.util.Log
import androidx.core.app.NotificationCompat
import androidx.core.app.ServiceCompat
import mobile.Mobile

/**
 * Foreground service whose sole purpose is to keep this process at
 * foreground priority so Android doesn't freeze/kill it (App Standby,
 * Doze, the Android 12+ cached-process freezer) while the user switches
 * to another app. The realm engine (libp2p host) runs as goroutines inside
 * this same process — without a foreground service, backgrounding the app
 * lets the OS suspend that process and the peer connection drops.
 *
 * Normally the engine is started by MainActivity.onCreate, which also runs
 * before this. But when BootCompletedReceiver starts this service directly
 * (boot autostart, see AndroidConfigPrefs), there is no MainActivity, so
 * this service starts the engine itself here too — Mobile.startServer is
 * idempotent and safe to call from both places (see its doc comment).
 *
 * This service does not itself stop the engine; that only happens when the
 * task is actually removed, i.e. the user swipes the app away from
 * recents.
 */
class RealmForegroundService : Service() {

	// Android drops incoming Wi-Fi multicast packets (including mDNS,
	// go-libp2p's LAN peer discovery) for any app that doesn't hold this
	// lock — without it, this peer can only ever be found by other group
	// members via the public DHT, never directly over the LAN even when
	// they're on the same network. Held for as long as the engine runs.
	private var multicastLock: WifiManager.MulticastLock? = null

	override fun onCreate() {
		super.onCreate()
		createNotificationChannel()
		showNotification()
		acquireMulticastLock()
		Thread {
			try {
				Mobile.startServer(filesDir.absolutePath, deviceName(), null, null)
			} catch (e: Exception) {
				Log.e(TAG, "failed to start server", e)
			}
		}.start()
	}

	private fun acquireMulticastLock() {
		val wifiManager = applicationContext.getSystemService(Context.WIFI_SERVICE) as WifiManager
		val lock = wifiManager.createMulticastLock(TAG)
		lock.setReferenceCounted(false)
		lock.acquire()
		multicastLock = lock
	}

	// Same fallback as MainActivity.deviceName(): Go's os.Hostname() always
	// returns "localhost" on Android.
	private fun deviceName(): String =
		Settings.Global.getString(contentResolver, Settings.Global.DEVICE_NAME) ?: Build.MODEL

	override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
		if (intent?.hasExtra(EXTRA_ENABLED) == true) {
			if (intent.getBooleanExtra(EXTRA_ENABLED, true)) {
				showNotification()
			} else {
				// Realm is off, so there are no peer connections left to keep
				// alive — drop the foreground notification. The service itself
				// keeps running so it can restore it if re-enabled.
				stopForeground(STOP_FOREGROUND_REMOVE)
			}
		}
		return START_STICKY
	}

	private fun showNotification() {
		val contentIntent = PendingIntent.getActivity(
			this,
			0,
			Intent(this, MainActivity::class.java).setFlags(
				Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TASK,
			),
			PendingIntent.FLAG_IMMUTABLE,
		)

		val notification = NotificationCompat.Builder(this, NOTIFICATION_CHANNEL_ID)
			.setSmallIcon(android.R.drawable.ic_dialog_info)
			.setContentTitle("Foilen Box")
			.setContentText("Keeping realm peer connections alive in the background")
			.setPriority(NotificationCompat.PRIORITY_LOW)
			.setOngoing(true)
			.setContentIntent(contentIntent)
			.build()

		if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
			ServiceCompat.startForeground(
				this,
				NOTIFICATION_ID,
				notification,
				ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC,
			)
		} else {
			startForeground(NOTIFICATION_ID, notification)
		}
	}

	override fun onBind(intent: Intent?): IBinder? = null

	// The task being removed (app swiped away from recents) is the user's
	// signal that they're done, unlike a plain app switch — so this is the
	// one place the engine actually gets torn down.
	override fun onTaskRemoved(rootIntent: Intent?) {
		Thread {
			try {
				Mobile.stopServer()
			} catch (e: Exception) {
				Log.w(TAG, "failed to stop server on task removal", e)
			}
		}.start()
		stopSelf()
		super.onTaskRemoved(rootIntent)
	}

	override fun onDestroy() {
		multicastLock?.let { if (it.isHeld) it.release() }
		multicastLock = null
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
		private const val NOTIFICATION_CHANNEL_ID = "realm_peer_service"
		private const val NOTIFICATION_ID = 2
		private const val EXTRA_ENABLED = "enabled"

		// The service is already running (started from MainActivity.onCreate)
		// by the time Realm can be toggled from the web UI, so a plain
		// startService (not startForegroundService) is enough to deliver this
		// command via onStartCommand.
		fun setRealmEnabled(context: Context, enabled: Boolean) {
			val intent = Intent(context, RealmForegroundService::class.java)
				.putExtra(EXTRA_ENABLED, enabled)
			context.startService(intent)
		}
	}
}
