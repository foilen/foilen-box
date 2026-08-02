package sms

// PlatformBridge is implemented on the Android side (see cmd/mobile.SmsBridge
// and MainActivity) and passed to Manager.SetBridge; nil on desktop, where
// sending/reading real SMS is impossible and notifications fall back to
// internal/notify (beeep) instead.
type PlatformBridge interface {
	// SendSms sends a real text message from this device.
	SendSms(phoneNumber, body string) error

	// ReadAllSms returns every SMS currently on this device (sent and
	// received) as a JSON array of SmsMessage-shaped objects, for the
	// one-time full-history import done when SMS management is first
	// enabled.
	ReadAllSms() (string, error)

	// ShowNotification displays a real OS notification; deepLink is
	// "groupId|storeName|phoneNumber" to open straight to that store's
	// conversation when it's clicked.
	ShowNotification(title, body, deepLink string)
}
