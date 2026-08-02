package com.foilen.box.android

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.provider.Telephony
import android.util.Log
import mobile.Mobile

/**
 * Notified immediately whenever this device gets a new text (RECEIVE_SMS
 * doesn't require being the default SMS app, unlike writing to the SMS
 * provider). No Activity is needed to react, same reasoning as
 * BootCompletedReceiver starting its service directly: forwards straight
 * into Go via the gomobile-exported Mobile.smsReceived, which records it
 * under whichever SMS-* realmmap this device currently manages (if any) —
 * see internal/sms.Manager.HandleIncomingSms.
 *
 * A single text can arrive as several PDUs (multipart SMS); the messages
 * from one intent all share the same originating address and are
 * concatenated in order into a single logical message.
 */
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
