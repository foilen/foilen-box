package com.foilen.box.android

import android.app.NotificationChannel
import android.app.NotificationManager
import android.content.Intent
import android.content.pm.ServiceInfo
import android.media.AudioFormat
import android.media.AudioRecord
import android.media.MediaCodec
import android.media.MediaCodecInfo
import android.media.MediaFormat
import android.media.MediaRecorder
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
import java.io.OutputStream
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
 *
 * Transport to the Go side (internal/camera.bridgeCapturer): a loopback TCP
 * connection carrying the raw Annex-B H.264 elementary stream. When a
 * microphone is also selected a second connection carries a stream of
 * length-prefixed (4-byte big-endian) AAC-LC access units, and each
 * connection is prefixed with a single tag byte ('V' / 'A') so the Go side
 * can tell them apart regardless of connection order.
 */
class CameraForegroundService : LifecycleService() {

	private val uiExecutor by lazy { ContextCompat.getMainExecutor(this) }
	private val encoderExecutor = Executors.newSingleThreadExecutor()
	private val audioExecutor = Executors.newSingleThreadExecutor()

	@Volatile private var capturing = false
	private var codec: MediaCodec? = null
	private var socket: Socket? = null
	private var cameraProvider: ProcessCameraProvider? = null

	@Volatile private var recordingAudio = false
	private var audioRecord: AudioRecord? = null
	private var audioCodec: MediaCodec? = null
	private var audioSocket: Socket? = null

	override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
		super.onStartCommand(intent, flags, startId)
		when (intent?.action) {
			ACTION_START -> {
				val deviceId = intent.getStringExtra(EXTRA_DEVICE_ID) ?: ""
				val audioDeviceId = intent.getStringExtra(EXTRA_AUDIO_DEVICE_ID) ?: ""
				val port = intent.getIntExtra(EXTRA_PORT, 0)
				val width = intent.getIntExtra(EXTRA_WIDTH, DEFAULT_CAPTURE_WIDTH)
				val height = intent.getIntExtra(EXTRA_HEIGHT, DEFAULT_CAPTURE_HEIGHT)
				val withAudio = audioDeviceId.isNotEmpty()
				showForegroundNotification(withAudio)
				// Off the main thread: connecting the capture socket (below) is
				// blocking I/O, which StrictMode forbids on the main thread even
				// to loopback.
				encoderExecutor.execute {
					if (withAudio) audioExecutor.execute { startAudioCapture(port) }
					startCapture(deviceId, port, width, height, withAudio)
				}
			}
			ACTION_STOP -> stopCaptureAndSelf()
		}
		return START_NOT_STICKY
	}

	private fun showForegroundNotification(withAudio: Boolean) {
		val channel = NotificationChannel(
			NOTIFICATION_CHANNEL_ID,
			"Camera streaming",
			NotificationManager.IMPORTANCE_LOW,
		).apply { description = "Active while the camera is being streamed over RTSP" }
		getSystemService(NotificationManager::class.java).createNotificationChannel(channel)

		val notification = NotificationCompat.Builder(this, NOTIFICATION_CHANNEL_ID)
			.setSmallIcon(android.R.drawable.ic_menu_camera)
			.setContentTitle("Foilen Box")
			.setContentText(if (withAudio) "Camera and microphone are being streamed" else "Camera is being streamed")
			.setPriority(NotificationCompat.PRIORITY_LOW)
			.setOngoing(true)
			.build()

		if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
			var type = ServiceInfo.FOREGROUND_SERVICE_TYPE_CAMERA
			if (withAudio) type = type or ServiceInfo.FOREGROUND_SERVICE_TYPE_MICROPHONE
			ServiceCompat.startForeground(this, NOTIFICATION_ID, notification, type)
		} else {
			startForeground(NOTIFICATION_ID, notification)
		}
	}

	private fun startCapture(deviceId: String, tcpPort: Int, width: Int, height: Int, tagged: Boolean) {
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
		if (tagged) {
			sock.getOutputStream().write(TAG_VIDEO.code)
			sock.getOutputStream().flush()
		}

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

	// startAudioCapture connects the tagged audio stream and pumps
	// AudioRecord PCM through an AAC-LC encoder to it. Failures here are
	// logged and left alone: the socket stays open (idle) so the Go side's
	// accept still succeeds and the video stream is unaffected.
	private fun startAudioCapture(tcpPort: Int) {
		val sock: Socket
		try {
			sock = Socket("127.0.0.1", tcpPort)
			sock.getOutputStream().write(TAG_AUDIO.code)
			sock.getOutputStream().flush()
		} catch (e: Exception) {
			Log.w(TAG, "failed to connect audio stream", e)
			return
		}
		audioSocket = sock

		val minBuf = AudioRecord.getMinBufferSize(
			AUDIO_SAMPLE_RATE,
			AudioFormat.CHANNEL_IN_STEREO,
			AudioFormat.ENCODING_PCM_16BIT,
		)
		val record = try {
			AudioRecord(
				MediaRecorder.AudioSource.MIC,
				AUDIO_SAMPLE_RATE,
				AudioFormat.CHANNEL_IN_STEREO,
				AudioFormat.ENCODING_PCM_16BIT,
				maxOf(minBuf, AUDIO_READ_BYTES * 2),
			)
		} catch (e: Exception) {
			Log.w(TAG, "failed to create AudioRecord (missing RECORD_AUDIO permission?)", e)
			return
		}
		if (record.state != AudioRecord.STATE_INITIALIZED) {
			Log.w(TAG, "AudioRecord did not initialize")
			record.release()
			return
		}

		val format = MediaFormat.createAudioFormat(MediaFormat.MIMETYPE_AUDIO_AAC, AUDIO_SAMPLE_RATE, AUDIO_CHANNEL_COUNT).apply {
			setInteger(MediaFormat.KEY_AAC_PROFILE, MediaCodecInfo.CodecProfileLevel.AACObjectLC)
			setInteger(MediaFormat.KEY_BIT_RATE, AUDIO_BIT_RATE)
			setInteger(MediaFormat.KEY_MAX_INPUT_SIZE, AUDIO_READ_BYTES)
		}
		val aac = MediaCodec.createEncoderByType(MediaFormat.MIMETYPE_AUDIO_AAC)
		aac.configure(format, null, null, MediaCodec.CONFIGURE_FLAG_ENCODE)
		aac.start()

		audioCodec = aac
		audioRecord = record
		recordingAudio = true
		record.startRecording()

		pumpAudio(record, aac, sock.getOutputStream())
	}

	// pumpAudio runs on audioExecutor: a synchronous read → encode → write
	// loop (independent of the Surface-driven video encoder, which must be
	// async). A dropped input buffer under back-pressure just skips that PCM
	// chunk, which is acceptable on a live stream.
	private fun pumpAudio(record: AudioRecord, codec: MediaCodec, out: OutputStream) {
		val pcm = ByteArray(AUDIO_READ_BYTES)
		val info = MediaCodec.BufferInfo()
		val lengthPrefix = ByteArray(4)
		try {
			while (recordingAudio) {
				val read = record.read(pcm, 0, pcm.size)
				if (read <= 0) continue

				var offset = 0
				while (offset < read) {
					val inIndex = codec.dequeueInputBuffer(DEQUEUE_TIMEOUT_US)
					if (inIndex < 0) break
					val inBuf = codec.getInputBuffer(inIndex) ?: break
					inBuf.clear()
					val chunk = minOf(inBuf.remaining(), read - offset)
					inBuf.put(pcm, offset, chunk)
					codec.queueInputBuffer(inIndex, 0, chunk, System.nanoTime() / 1000, 0)
					offset += chunk
				}

				var outIndex = codec.dequeueOutputBuffer(info, 0)
				while (outIndex >= 0) {
					val isConfig = info.flags and MediaCodec.BUFFER_FLAG_CODEC_CONFIG != 0
					if (!isConfig && info.size > 0) {
						val outBuf = codec.getOutputBuffer(outIndex)
						if (outBuf != null) {
							outBuf.position(info.offset)
							outBuf.limit(info.offset + info.size)
							val au = ByteArray(info.size)
							outBuf.get(au)
							lengthPrefix[0] = (au.size ushr 24).toByte()
							lengthPrefix[1] = (au.size ushr 16).toByte()
							lengthPrefix[2] = (au.size ushr 8).toByte()
							lengthPrefix[3] = au.size.toByte()
							out.write(lengthPrefix)
							out.write(au)
						}
					}
					codec.releaseOutputBuffer(outIndex, false)
					outIndex = codec.dequeueOutputBuffer(info, 0)
				}
			}
		} catch (e: Exception) {
			if (recordingAudio) Log.w(TAG, "audio capture loop ended", e)
		}
	}

	private fun stopAudioCapture() {
		recordingAudio = false
		audioRecord?.let {
			try {
				it.stop()
			} catch (e: Exception) {
				// Already stopped/errored.
			}
			it.release()
		}
		audioRecord = null
		audioCodec?.let {
			try {
				it.stop()
			} catch (e: Exception) {
				// Already stopped/errored.
			}
			it.release()
		}
		audioCodec = null
		audioSocket?.let {
			try {
				it.close()
			} catch (e: Exception) {
				// Ignore.
			}
		}
		audioSocket = null
	}

	private fun stopCaptureAndSelf() {
		capturing = false
		stopAudioCapture()
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

		// Must match internal/camera/mpegts.go's audioSampleRate / audioChannelCount.
		private const val AUDIO_SAMPLE_RATE = 48000
		private const val AUDIO_CHANNEL_COUNT = 2
		private const val AUDIO_BIT_RATE = 128_000
		private const val AUDIO_READ_BYTES = 8192
		private const val DEQUEUE_TIMEOUT_US = 10_000L

		private const val TAG_VIDEO = 'V'
		private const val TAG_AUDIO = 'A'

		const val ACTION_START = "com.foilen.box.android.camera.START"
		const val ACTION_STOP = "com.foilen.box.android.camera.STOP"
		const val EXTRA_DEVICE_ID = "deviceId"
		const val EXTRA_AUDIO_DEVICE_ID = "audioDeviceId"
		const val EXTRA_PORT = "port"
		const val EXTRA_WIDTH = "width"
		const val EXTRA_HEIGHT = "height"
	}
}
