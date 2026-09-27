package sms

type PlatformBridge interface {
	SendSms(phoneNumber, body string) error

	ReadAllSms() (string, error)

	ShowNotification(title, body, deepLink string)
}
