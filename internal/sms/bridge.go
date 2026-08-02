package sms

// PlatformBridge is implemented on Android (cmd/mobile.SmsBridge, MainActivity)
// and passed to Manager.SetBridge; nil on desktop, where notifications fall
// back to internal/notify instead.
type PlatformBridge interface {
	// SendSms sends a real text message from this device.
	SendSms(phoneNumber, body string) error

	// ReadAllSms returns every SMS on this device as a JSON array of
	// SmsMessage-shaped objects, for the full-history import on first enable.
	ReadAllSms() (string, error)

	// ShowNotification displays an OS notification; deepLink is
	// "groupId|storeName|phoneNumber" to open that conversation on click.
	ShowNotification(title, body, deepLink string)
}
