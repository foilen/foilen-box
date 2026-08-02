// Package notify shows a desktop OS notification via gen2brain/beeep. It has
// no click-to-open action: beeep doesn't expose a portable click callback
// across Linux/Windows/macOS, so this is plain "fire and show" — unlike the
// Android side (internal/sms.PlatformBridge.ShowNotification), which gets a
// real click-to-open deep link via a PendingIntent.
package notify

import "github.com/gen2brain/beeep"

func init() {
	// beeep.AppName defaults to "DefaultAppName", which is what the OS
	// notification shows as the sending app's identity (separate from the
	// title/body passed to Notify).
	beeep.AppName = "Foilen Box"
}

// Notify shows title/body as a desktop notification.
func Notify(title, body string) error {
	return beeep.Notify(title, body, "")
}
