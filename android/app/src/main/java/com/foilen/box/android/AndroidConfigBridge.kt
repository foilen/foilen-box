package com.foilen.box.android

import android.content.Context
import android.webkit.JavascriptInterface

/**
 * Exposed to the WebView as `window.AndroidConfigBridge` (see
 * MainActivity.onCreate) so the web UI's Android tab
 * (js/android-config.js) can read/persist AndroidConfigPrefs. Runs on the
 * WebView's JS thread, not the UI thread, but SharedPreferences access is
 * thread-safe.
 */
class AndroidConfigBridge(private val context: Context) {

	@JavascriptInterface
	fun isBootAutostartEnabled(): Boolean = AndroidConfigPrefs.isBootAutostartEnabled(context)

	@JavascriptInterface
	fun setBootAutostartEnabled(enabled: Boolean) {
		AndroidConfigPrefs.setBootAutostartEnabled(context, enabled)
	}
}
