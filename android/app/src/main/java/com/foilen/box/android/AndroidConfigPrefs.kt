package com.foilen.box.android

import android.content.Context

/**
 * Persists the Android-only settings exposed on the web UI's "Android" tab
 * (see js/android-config.js), read back by BootCompletedReceiver.
 */
object AndroidConfigPrefs {

	private const val PREFS_NAME = "android_config"
	private const val KEY_BOOT_AUTOSTART = "boot_autostart"

	// Set true while RealmForegroundService is meant to be alive, cleared only
	// when the user swipes the app away. Read by WatchdogReceiver to decide
	// whether to resurrect the service after the OS kills it.
	private const val KEY_SERVICE_EXPECTED = "service_expected"

	fun isBootAutostartEnabled(context: Context): Boolean =
		context.getSharedPreferences(PREFS_NAME, Context.MODE_PRIVATE).getBoolean(KEY_BOOT_AUTOSTART, false)

	fun setBootAutostartEnabled(context: Context, enabled: Boolean) {
		context.getSharedPreferences(PREFS_NAME, Context.MODE_PRIVATE).edit()
			.putBoolean(KEY_BOOT_AUTOSTART, enabled)
			.apply()
	}

	fun isServiceExpected(context: Context): Boolean =
		context.getSharedPreferences(PREFS_NAME, Context.MODE_PRIVATE).getBoolean(KEY_SERVICE_EXPECTED, false)

	fun setServiceExpected(context: Context, expected: Boolean) {
		context.getSharedPreferences(PREFS_NAME, Context.MODE_PRIVATE).edit()
			.putBoolean(KEY_SERVICE_EXPECTED, expected)
			.apply()
	}
}
