//go:build windows

package notify

import (
	toast "git.sr.ht/~jackmordaunt/go-toast"
	"github.com/gen2brain/beeep"
)

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
