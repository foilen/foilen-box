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
