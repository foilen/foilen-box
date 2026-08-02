//go:build darwin

package notify

import (
	"os/exec"

	"github.com/gen2brain/beeep"
)

// NotifyClick shows a notification that opens url when clicked, via
// terminal-notifier's -open flag. Falls back to a plain notification if
// terminal-notifier isn't installed (beeep's osascript path has no click support).
func NotifyClick(title, body, url string) error {
	path, err := exec.LookPath("terminal-notifier")
	if err != nil {
		return Notify(title, body)
	}
	cmd := exec.Command(path, "-title", title, "-message", body, "-open", url, "-group", beeep.AppName)
	if err := cmd.Run(); err != nil {
		return Notify(title, body)
	}
	return nil
}
