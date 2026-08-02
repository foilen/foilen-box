package main

import (
	_ "embed"
	"log"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/getlantern/systray"

	"foilen-box/internal/browseropen"
	"foilen-box/internal/webserver"
)

//go:embed systray.png
var systrayIcon []byte

func run(server *webserver.Server) {
	if !hasDisplay() {
		// systray.Run() initializes GTK, which calls exit() itself (unrecoverable
		// via panic/recover) when no display is available on Linux.
		log.Printf("no display detected, running headless without a systray icon")
		runHeadless(server)
		return
	}

	systray.Run(func() {
		onReady(server)
	}, func() {
		if err := server.Stop(); err != nil {
			log.Printf("failed to stop web server: %v", err)
		}
	})
}

// hasDisplay reports whether a graphical display is available. Only Linux
// needs this check: macOS/Windows systray backends don't hard-exit without one.
func hasDisplay() bool {
	if runtime.GOOS != "linux" {
		return true
	}
	return os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != ""
}

func runHeadless(server *webserver.Server) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	if err := server.Stop(); err != nil {
		log.Printf("failed to stop web server: %v", err)
	}
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
