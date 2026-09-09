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
import android.provider.Settings
import android.util.Log
import androidx.core.app.NotificationCompat
import androidx.core.app.ServiceCompat
import mobile.Mobile

/**
 * Keeps this process at foreground priority so Android doesn't freeze/kill it
 * (App Standby, Doze, the cached-process freezer) while the realm engine's
 * goroutines run in the background.
 *
 * Also starts the engine itself, since BootCompletedReceiver may start this
 * service directly (boot autostart) with no MainActivity around;
 * Mobile.startServer is idempotent and safe to call from both places.
 *
 * The engine keeps running for the life of the process. It is NOT torn down
 * on onTaskRemoved: that callback also fires when the OS trims the
 * backgrounded task under memory pressure, and treating it as "the user is
 * done" is what previously left the box silently offline for hours. The
 * only way to stop it is the explicit Realm on/off toggle in the web UI
 * (setRealmEnabled), or a force-stop / disabling boot autostart + reboot.
 */
class RealmForegroundService : Service() {

	// Without this lock, Android drops incoming Wi-Fi multicast (mDNS/LAN peer
	// discovery), forcing peer discovery through the public DHT only.
	private var multicastLock: WifiManager.MulticastLock? = null

	private val handler = Handler(Looper.getMainLooper())

	// Peer counts aren't push-notified from Go, so the notification text is
	// kept fresh by polling Mobile.connectedPeersCount/peersTotalCount.
	// Same tick also drives the multicast lock duty cycle below.
	private var dutyCycleTick = 0

	private val peerCountRefresher = object : Runnable {
		override fun run() {
			updateNotification()
			refreshMulticastLockDutyCycle()
			handler.postDelayed(this, PEER_COUNT_REFRESH_MS)
		}
	}

	// Peers stay connected over their own established sockets — the multicast
	// lock is only needed to *discover new* peers via mDNS, not to keep
	// existing ones alive. Holding it permanently disables the Wi-Fi radio's
	// hardware multicast filter, so it wakes the radio/CPU for every LAN
	// mDNS/SSDP/broadcast packet, even overnight with a stable, fully
	// connected peer set. Instead, duty-cycle it: acquire only for one tick
	// out of every DUTY_CYCLE_TICKS, which still gives regular chances to
	// discover new LAN peers without holding it open continuously.
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
		// Must call startForeground promptly after startForegroundService,
		// even if Realm was turned off — drop it again right after.
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
					deviceName(),
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

	// Same fallback as MainActivity.deviceName().
	private fun deviceName(): String =
		Settings.Global.getString(contentResolver, Settings.Global.DEVICE_NAME) ?: Build.MODEL

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
				// Realm is off, so there are no peer connections left to keep
				// alive — drop the foreground notification and stop expecting
				// the service, so the watchdog stops resurrecting it and a
				// START_STICKY restart doesn't bring it back. The service
				// itself keeps running so it can restore everything if
				// re-enabled in this same process.
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

	// Refreshes the already-shown foreground notification with current peer
	// counts, without going through startForeground again.
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

	// Safety net for FGS types the OS may later start enforcing an execution
	// time limit on (only dataSync/mediaProcessing today, not connectedDevice) —
	// stop cleanly instead of getting force-killed with
	// ForegroundServiceDidNotStopInTimeException.
	override fun onTimeout(startId: Int, fgsType: Int) {
		stopForeground(STOP_FOREGROUND_REMOVE)
		stopSelf(startId)
	}

	override fun onBind(intent: Intent?): IBinder? = null

	// Deliberately does NOT tear down the engine. onTaskRemoved fires both
	// on a genuine swipe-away and when the OS trims the backgrounded task,
	// and the two are indistinguishable here; stopping the engine on the
	// latter is what left the box offline for hours. Re-assert the watchdog
	// (in case this arrived on a service the OS restarted) and let the
	// engine keep running until an explicit Realm-off toggle.
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

		// Process-local liveness flag: false whenever this process was never
		// started or was killed (statics reset with the process), which is
		// exactly when WatchdogWorker should restart the service.
		@Volatile
		var isRunning = false
			private set
		private const val NOTIFICATION_CHANNEL_ID = "realm_peer_service"
		private const val NOTIFICATION_ID = 2
		private const val EXTRA_ENABLED = "enabled"
		private const val PEER_COUNT_REFRESH_MS = 5 * 60_000L

		// Multicast lock duty cycle: held for 1 tick out of every 6
		// (~5 min on / ~25 min off at PEER_COUNT_REFRESH_MS = 5 min).
		private const val DUTY_CYCLE_TICKS = 6

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
