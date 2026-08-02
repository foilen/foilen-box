//go:build windows

package notify

import (
	toast "git.sr.ht/~jackmordaunt/go-toast"
	"github.com/gen2brain/beeep"
)

// NotifyClick shows title/body as a desktop notification that opens url in
// the default browser when clicked. ActivationType Protocol tells Windows
// to ShellExecute ActivationArguments (the url) directly when the toast is
// clicked, unlike the default Foreground activation type, which requires
// registering an in-process COM activator/AUMID we don't have. Falls back
// to a plain (non-clickable) notification if pushing the toast fails.
func NotifyClick(title, body, url string) error {
	n := toast.Notification{
		AppID:               beeep.AppName,
		Title:               title,
		Body:                body,
		ActivationType:      toast.Protocol,
		ActivationArguments: url,
	}
	if err := n.Push(); err != nil {
		return Notify(title, body)
	}
	return nil
}
