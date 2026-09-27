package com.foilen.box.android

import android.content.Context
import android.webkit.JavascriptInterface

class AndroidConfigBridge(private val context: Context) {

	@JavascriptInterface
	fun isBootAutostartEnabled(): Boolean = AndroidConfigPrefs.isBootAutostartEnabled(context)

	@JavascriptInterface
	fun setBootAutostartEnabled(enabled: Boolean) {
		AndroidConfigPrefs.setBootAutostartEnabled(context, enabled)
	}
}
