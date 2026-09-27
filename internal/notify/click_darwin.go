//go:build darwin

package notify

import (
	"os/exec"

	"github.com/gen2brain/beeep"
)

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
