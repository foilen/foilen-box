// Package browseropen launches the OS default browser at a URL.
package browseropen

import (
	"bytes"
	"fmt"
	"os/exec"
	"runtime"
)

// Open launches the OS default browser at the given URL.
func Open(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	// Run (not Start) and capture stderr: the launcher command (e.g.
	// xdg-open) can exit non-zero after starting fine - e.g. no default
	// browser configured - and Start alone would silently miss that, since
	// it only fails when the executable itself can't be found.
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to open browser: %w: %s", err, stderr.String())
	}
	return nil
}
