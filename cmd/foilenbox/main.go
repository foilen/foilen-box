package main

import (
	"log"
	"time"

	"foilen-box/internal/webserver"
	realmmodel "foilen-realm/model"
)

func main() {
	time.Local = time.UTC

	server, err := webserver.Start("", realmmodel.DhtModeServer, "")
	if err != nil {
		log.Fatalf("failed to start web server: %v", err)
	}
	log.Printf("Foilen Box UI available at %s", server.URL())

	run(server)
}
