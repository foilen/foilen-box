//go:build darwin

package notify

import (
	"os/exec"

	"github.com/gen2brain/beeep"
)

// NotifyClick shows title/body as a desktop notification that opens url in
// the default browser when clicked, via terminal-notifier's -open flag
// (which itself invokes `open <url>` when the notification is clicked - no
// in-process click handling needed). Falls back to a plain (non-clickable)
// notification if terminal-notifier isn't installed: beeep's other macOS
// path (osascript) has no click action support.
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
