//go:build windows

package notify

import (
	toast "git.sr.ht/~jackmordaunt/go-toast"
	"github.com/gen2brain/beeep"
)

// NotifyClick shows a notification that opens url when clicked.
// ActivationType Protocol makes Windows ShellExecute the url directly,
// avoiding the default Foreground type's COM activator/AUMID registration.
// Falls back to a plain notification if pushing the toast fails.
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
