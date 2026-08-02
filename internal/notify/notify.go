// Package notify shows a desktop OS notification via gen2brain/beeep. It has
// no click-to-open action since beeep exposes no portable click callback
// across platforms — see NotifyClick for the per-OS workarounds.
package notify

import "github.com/gen2brain/beeep"

func init() {
	// beeep.AppName is shown as the sending app's identity; defaults to "DefaultAppName".
	beeep.AppName = "Foilen Box"
}

// Notify shows title/body as a desktop notification.
func Notify(title, body string) error {
	return beeep.Notify(title, body, "")
}
