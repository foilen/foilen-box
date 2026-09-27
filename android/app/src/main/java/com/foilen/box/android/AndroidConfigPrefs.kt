package com.foilen.box.android

import android.content.Context

object AndroidConfigPrefs {

	private const val PREFS_NAME = "android_config"
	private const val KEY_BOOT_AUTOSTART = "boot_autostart"

	private const val KEY_SERVICE_EXPECTED = "service_expected"

	fun isBootAutostartEnabled(context: Context): Boolean =
		context.getSharedPreferences(PREFS_NAME, Context.MODE_PRIVATE).getBoolean(KEY_BOOT_AUTOSTART, false)

	fun setBootAutostartEnabled(context: Context, enabled: Boolean) {
		context.getSharedPreferences(PREFS_NAME, Context.MODE_PRIVATE).edit()
			.putBoolean(KEY_BOOT_AUTOSTART, enabled)
			.apply()
	}

	private const val KEY_REALM_ENABLED = "realm_enabled"

	fun isRealmEnabled(context: Context): Boolean =
		context.getSharedPreferences(PREFS_NAME, Context.MODE_PRIVATE).getBoolean(KEY_REALM_ENABLED, true)

	fun setRealmEnabled(context: Context, enabled: Boolean) {
		context.getSharedPreferences(PREFS_NAME, Context.MODE_PRIVATE).edit()
			.putBoolean(KEY_REALM_ENABLED, enabled)
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
