package com.foilen.box.android

import android.app.NotificationChannel
import android.app.NotificationManager
import android.content.Intent
import android.content.pm.ServiceInfo
import android.media.MediaCodec
import android.media.MediaCodecInfo
import android.media.MediaFormat
import android.os.Build
import android.util.Log
import android.util.Size
import androidx.camera.core.CameraSelector
import androidx.camera.core.Preview
import androidx.camera.core.resolutionselector.ResolutionSelector
import androidx.camera.core.resolutionselector.ResolutionStrategy
import androidx.camera.lifecycle.ProcessCameraProvider
import androidx.core.app.NotificationCompat
import androidx.core.app.ServiceCompat
import androidx.core.content.ContextCompat
import androidx.lifecycle.LifecycleService
import java.net.Socket
import java.util.concurrent.Executors

/**
 * Hosts the actual CameraX + MediaCodec capture pipeline for the Camera/RTSP
 * feature, as a foreground Service rather than tied to MainActivity — so the
 * stream keeps working with the screen off and the app backgrounded/not in
 * recents' foreground, same spirit as RealmForegroundService keeping the
 * realm engine alive. CameraX's bindToLifecycle needs a LifecycleOwner;
 * LifecycleService provides one without needing an Activity around
 * (the officially recommended way to run CameraX from a Service).
 *
 * Started only while internal/camera.Manager actually has an RTSP viewer
 * (see CameraCaptureBridge.startCapture/stopCapture) — promoted to
 * foreground for exactly that duration, then stops itself, so the
 * camera-in-use indicator/notification only ever shows while true.
 */
class CameraForegroundService : LifecycleService() {

	private val uiExecutor by lazy { ContextCompat.getMainExecutor(this) }
	private val encoderExecutor = Executors.newSingleThreadExecutor()

	@Volatile private var capturing = false
	private var codec: MediaCodec? = null
	private var socket: Socket? = null
	private var cameraProvider: ProcessCameraProvider? = null

	override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
		super.onStartCommand(intent, flags, startId)
		when (intent?.action) {
			ACTION_START -> {
				showForegroundNotification()
				val deviceId = intent.getStringExtra(EXTRA_DEVICE_ID) ?: ""
				val port = intent.getIntExtra(EXTRA_PORT, 0)
				val width = intent.getIntExtra(EXTRA_WIDTH, DEFAULT_CAPTURE_WIDTH)
				val height = intent.getIntExtra(EXTRA_HEIGHT, DEFAULT_CAPTURE_HEIGHT)
				// Off the main thread: connecting the capture socket (below) is
				// blocking I/O, which StrictMode forbids on the main thread even
				// to loopback.
				encoderExecutor.execute { startCapture(deviceId, port, width, height) }
			}
			ACTION_STOP -> stopCaptureAndSelf()
		}
		return START_NOT_STICKY
	}

	private fun showForegroundNotification() {
		val channel = NotificationChannel(
			NOTIFICATION_CHANNEL_ID,
			"Camera streaming",
			NotificationManager.IMPORTANCE_LOW,
		).apply { description = "Active while the camera is being streamed over RTSP" }
		getSystemService(NotificationManager::class.java).createNotificationChannel(channel)

		val notification = NotificationCompat.Builder(this, NOTIFICATION_CHANNEL_ID)
			.setSmallIcon(android.R.drawable.ic_menu_camera)
			.setContentTitle("Foilen Box")
			.setContentText("Camera is being streamed")
			.setPriority(NotificationCompat.PRIORITY_LOW)
			.setOngoing(true)
			.build()

		if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
			ServiceCompat.startForeground(this, NOTIFICATION_ID, notification, ServiceInfo.FOREGROUND_SERVICE_TYPE_CAMERA)
		} else {
			startForeground(NOTIFICATION_ID, notification)
		}
	}

	private fun startCapture(deviceId: String, tcpPort: Int, width: Int, height: Int) {
		capturing = true
		val selector = if (deviceId == "front") CameraSelector.DEFAULT_FRONT_CAMERA else CameraSelector.DEFAULT_BACK_CAMERA

		val format = MediaFormat.createVideoFormat(MediaFormat.MIMETYPE_VIDEO_AVC, width, height).apply {
			setInteger(MediaFormat.KEY_COLOR_FORMAT, MediaCodecInfo.CodecCapabilities.COLOR_FormatSurface)
			setInteger(MediaFormat.KEY_BIT_RATE, BIT_RATE)
			setInteger(MediaFormat.KEY_FRAME_RATE, FRAME_RATE)
			setInteger(MediaFormat.KEY_I_FRAME_INTERVAL, 1)
		}
		val mediaCodec = MediaCodec.createEncoderByType(MediaFormat.MIMETYPE_VIDEO_AVC)
		mediaCodec.configure(format, null, null, MediaCodec.CONFIGURE_FLAG_ENCODE)
		val inputSurface = mediaCodec.createInputSurface()

		// Connecting before starting the encoder so the first output buffers
		// (SPS/PPS/IDR) always have somewhere to go.
		val sock = Socket("127.0.0.1", tcpPort)

		mediaCodec.setCallback(object : MediaCodec.Callback() {
			override fun onInputBufferAvailable(codec: MediaCodec, index: Int) {}

			override fun onOutputBufferAvailable(codec: MediaCodec, index: Int, info: MediaCodec.BufferInfo) {
				// setCallback(cb, null) below runs callbacks on the calling thread only if
				// it has a Looper; encoderExecutor's thread doesn't, so this actually runs
				// on the main thread — copy the bytes out and release the buffer here, then
				// do the blocking socket write on encoderExecutor (single-threaded, so
				// writes still land in frame order).
				try {
					val buffer = codec.getOutputBuffer(index)
					if (buffer != null && info.size > 0) {
						val bytes = ByteArray(info.size)
						buffer.get(bytes)
						encoderExecutor.execute {
							try {
								sock.getOutputStream().write(bytes)
							} catch (e: Exception) {
								Log.w(TAG, "camera encoder output write failed", e)
							}
						}
					}
				} finally {
					try {
						codec.releaseOutputBuffer(index, false)
					} catch (e: Exception) {
						// Codec may already be stopped/released by a concurrent stopCapture.
					}
				}
			}

			override fun onError(codec: MediaCodec, e: MediaCodec.CodecException) {
				Log.w(TAG, "camera encoder error", e)
			}

			override fun onOutputFormatChanged(codec: MediaCodec, format: MediaFormat) {}
		}, null)

		mediaCodec.start()
		codec = mediaCodec
		socket = sock

		val providerFuture = ProcessCameraProvider.getInstance(this)
		providerFuture.addListener({
			if (!capturing) return@addListener // stopCapture already ran
			try {
				val provider = providerFuture.get()
				provider.unbindAll()
				// Pin the preview to the encoder's fixed input-surface size (width x height);
				// otherwise CameraX picks its own resolution and the camera frames end up
				// scaled/pillarboxed within that surface instead of filling it.
				val resolutionSelector = ResolutionSelector.Builder()
					.setResolutionStrategy(
						ResolutionStrategy(Size(width, height), ResolutionStrategy.FALLBACK_RULE_CLOSEST_HIGHER_THEN_LOWER),
					)
					.build()
				val preview = Preview.Builder().setResolutionSelector(resolutionSelector).build()
				preview.setSurfaceProvider(encoderExecutor) { request -> request.provideSurface(inputSurface, encoderExecutor) {} }
				provider.bindToLifecycle(this, selector, preview)
				cameraProvider = provider
			} catch (e: Exception) {
				Log.w(TAG, "failed to bind camera", e)
			}
		}, uiExecutor)
	}

	private fun stopCaptureAndSelf() {
		capturing = false
		cameraProvider?.unbindAll()
		cameraProvider = null
		codec?.let {
			try {
				it.stop()
			} catch (e: Exception) {
				// Already stopped/errored; nothing to clean up further.
			}
			try {
				it.release()
			} catch (e: Exception) {
				// Ignore.
			}
		}
		codec = null
		socket?.let {
			try {
				it.close()
			} catch (e: Exception) {
				// Ignore.
			}
		}
		socket = null
		stopForeground(STOP_FOREGROUND_REMOVE)
		stopSelf()
	}

	override fun onDestroy() {
		stopCaptureAndSelf()
		super.onDestroy()
	}

	companion object {
		private const val TAG = "FoilenBoxCamera"
		private const val NOTIFICATION_CHANNEL_ID = "camera_streaming"
		private const val NOTIFICATION_ID = 3
		private const val DEFAULT_CAPTURE_WIDTH = 1280
		private const val DEFAULT_CAPTURE_HEIGHT = 720
		private const val BIT_RATE = 2_000_000
		private const val FRAME_RATE = 15

		const val ACTION_START = "com.foilen.box.android.camera.START"
		const val ACTION_STOP = "com.foilen.box.android.camera.STOP"
		const val EXTRA_DEVICE_ID = "deviceId"
		const val EXTRA_PORT = "port"
		const val EXTRA_WIDTH = "width"
		const val EXTRA_HEIGHT = "height"
	}
}
