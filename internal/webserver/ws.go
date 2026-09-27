package webserver

import (
	"log"
	"net/http"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

type authMessage struct {
	Token string `json:"token"`
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("webserver: ws upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	var auth authMessage
	if err := conn.ReadJSON(&auth); err != nil || auth.Token != s.token {
		return
	}

	for {
		var req request
		if err := conn.ReadJSON(&req); err != nil {
			return
		}
		resp := s.api.dispatch(req)
		if err := conn.WriteJSON(resp); err != nil {
			return
		}
	}
}
