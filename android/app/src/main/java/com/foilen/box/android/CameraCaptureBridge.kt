package com.foilen.box.android

import android.content.Context
import android.content.Intent
import androidx.camera.core.CameraSelector
import androidx.camera.lifecycle.ProcessCameraProvider
import androidx.core.content.ContextCompat
import mobile.CameraBridge
import org.json.JSONArray
import org.json.JSONObject
import java.util.concurrent.TimeUnit

/**
 * Implements mobile.CameraBridge by starting/stopping
 * CameraForegroundService, which hosts the actual CameraX/MediaCodec
 * capture pipeline — kept independent of any Activity (only needs a
 * Context) precisely so the camera keeps streaming with the screen off and
 * the app not in the foreground; see CameraForegroundService's doc for why
 * a Service, not MainActivity, owns the CameraX lifecycle. Instantiated by
 * both MainActivity and RealmForegroundService so the bridge is available
 * however the app/engine was started (including the boot-autostart path,
 * with no MainActivity ever opened).
 */
class CameraCaptureBridge(context: Context) : CameraBridge {

	private val appContext = context.applicationContext

	override fun listCameras(): String {
		val provider = ProcessCameraProvider.getInstance(appContext).get(5, TimeUnit.SECONDS)
		val result = JSONArray()
		if (provider.hasCamera(CameraSelector.DEFAULT_BACK_CAMERA)) {
			result.put(JSONObject().apply { put("id", "back"); put("label", "Back Camera") })
		}
		if (provider.hasCamera(CameraSelector.DEFAULT_FRONT_CAMERA)) {
			result.put(JSONObject().apply { put("id", "front"); put("label", "Front Camera") })
		}
		return result.toString()
	}

	// The platform exposes a single logical capture input (AudioSource.MIC);
	// the OS routes it to whatever physical mic it deems primary.
	override fun listMicrophones(): String =
		JSONArray().put(JSONObject().apply { put("id", "mic"); put("label", "Microphone") }).toString()

	override fun startCapture(deviceId: String, audioDeviceId: String, tcpPort: Int, width: Int, height: Int) {
		val intent = Intent(appContext, CameraForegroundService::class.java)
			.setAction(CameraForegroundService.ACTION_START)
			.putExtra(CameraForegroundService.EXTRA_DEVICE_ID, deviceId)
			.putExtra(CameraForegroundService.EXTRA_AUDIO_DEVICE_ID, audioDeviceId)
			.putExtra(CameraForegroundService.EXTRA_PORT, tcpPort)
			.putExtra(CameraForegroundService.EXTRA_WIDTH, width)
			.putExtra(CameraForegroundService.EXTRA_HEIGHT, height)
		ContextCompat.startForegroundService(appContext, intent)
	}

	override fun stopCapture() {
		val intent = Intent(appContext, CameraForegroundService::class.java)
			.setAction(CameraForegroundService.ACTION_STOP)
		appContext.startService(intent)
	}
}
