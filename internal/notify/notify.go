package notify

import "github.com/gen2brain/beeep"

func init() {
	beeep.AppName = "Foilen Box"
}

func Notify(title, body string) error {
	return beeep.Notify(title, body, "")
}
