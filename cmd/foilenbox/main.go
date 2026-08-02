// Command foilenbox is the desktop app entry point: it starts the embedded
// web UI/API server (internal/webserver) and puts a systray icon in the
// system tray to open it in the default browser or quit.
package main

import (
	"log"
	"time"

	"foilen-box/internal/webserver"
	realmmodel "foilen-realm/model"
)

func main() {
	time.Local = time.UTC // matches original Java app's TimeZone.setDefault(UTC)

	server, err := webserver.Start("", realmmodel.DhtModeServer, "")
	if err != nil {
		log.Fatalf("failed to start web server: %v", err)
	}
	log.Printf("Foilen Box UI available at %s", server.URL())

	run(server)
}
