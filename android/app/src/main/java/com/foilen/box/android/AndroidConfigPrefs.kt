package com.foilen.box.android

import android.content.Context

/**
 * Persists the Android-only settings exposed on the web UI's "Android" tab
 * (see js/android-config.js), read back by BootCompletedReceiver.
 */
object AndroidConfigPrefs {

	private const val PREFS_NAME = "android_config"
	private const val KEY_BOOT_AUTOSTART = "boot_autostart"

	fun isBootAutostartEnabled(context: Context): Boolean =
		context.getSharedPreferences(PREFS_NAME, Context.MODE_PRIVATE).getBoolean(KEY_BOOT_AUTOSTART, false)

	fun setBootAutostartEnabled(context: Context, enabled: Boolean) {
		context.getSharedPreferences(PREFS_NAME, Context.MODE_PRIVATE).edit()
			.putBoolean(KEY_BOOT_AUTOSTART, enabled)
			.apply()
	}
}
