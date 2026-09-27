package com.foilen.box.android

import android.content.Context
import android.os.Build
import android.provider.Settings

fun deviceName(context: Context): String =
	Settings.Global.getString(context.contentResolver, Settings.Global.DEVICE_NAME) ?: Build.MODEL
