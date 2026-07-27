package main

import (
	_ "embed"
	"fmt"
	"log"

	"github.com/gen2brain/beeep"
	"github.com/getlantern/systray"

	"foilen-box/internal/browseropen"
	"foilen-box/internal/webserver"
)

//go:embed systray.png
var systrayIcon []byte

// desktopNotificationSink pops a native OS notification for every Realm
// notification received, independent of whether the web UI is open.
type desktopNotificationSink struct{}

func (desktopNotificationSink) Notify(from, title, body string) {
	if err := beeep.Notify(title, fmt.Sprintf("%s\n\nfrom %s", body, from), ""); err != nil {
		log.Printf("failed to show desktop notification: %v", err)
	}
}

func run(server *webserver.Server) {
	systray.Run(func() {
		onReady(server)
	}, func() {
		if err := server.Stop(); err != nil {
			log.Printf("failed to stop web server: %v", err)
		}
	})
}

func onReady(server *webserver.Server) {
	systray.SetIcon(systrayIcon)
	systray.SetTitle("Box")
	systray.SetTooltip("Foilen Box")

	openItem := systray.AddMenuItem("Open", "Open the Foilen Box UI in your browser")
	quitItem := systray.AddMenuItem("Quit", "Quit Foilen Box")

	go func() {
		for {
			select {
			case <-openItem.ClickedCh:
				if err := browseropen.Open(server.URL()); err != nil {
					log.Printf("failed to open browser: %v", err)
				}
			case <-quitItem.ClickedCh:
				systray.Quit()
				return
			}
		}
	}()
}
