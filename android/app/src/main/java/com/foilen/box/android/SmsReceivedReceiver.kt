package com.foilen.box.android

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.provider.Telephony
import android.util.Log
import mobile.Mobile

class SmsReceivedReceiver : BroadcastReceiver() {

	override fun onReceive(context: Context, intent: Intent) {
		if (intent.action != Telephony.Sms.Intents.SMS_RECEIVED_ACTION) return

		val messages = Telephony.Sms.Intents.getMessagesFromIntent(intent)
		if (messages.isNullOrEmpty()) return

		val sender = messages[0].originatingAddress ?: return
		val body = messages.joinToString(separator = "") { it.messageBody ?: "" }
		val timestampMillis = messages[0].timestampMillis

		try {
			Mobile.smsReceived(sender, body, timestampMillis)
		} catch (e: Exception) {
			Log.e(TAG, "failed to forward received SMS", e)
		}
	}

	companion object {
		private const val TAG = "FoilenBox"
	}
}
