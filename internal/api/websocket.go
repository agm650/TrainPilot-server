package api

import (
	"net/http"

	ws "github.com/agm650/TrainPilot-server/internal/websocket"
)

func (s *Server) eventsWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := ws.Accept(w, r)
	if err != nil {
		return
	}
	defer conn.Close()
	ch, unsubscribe := s.events.Subscribe(64)
	defer unsubscribe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			var incoming struct {
				Type string `json:"type"`
			}
			if err := conn.ReadJSON(&incoming); err != nil {
				return
			}
			if incoming.Type == "client.heartbeat" {
				now := s.events.Publish("client.heartbeat", map[string]string{"sessionId": sessionFrom(r).ID}).Timestamp
				_ = s.store.TouchSession(r.Context(), sessionFrom(r).ID, now)
			}
		}
	}()
	if err := conn.WriteJSON(map[string]any{"type": "system.snapshot", "sequence": 0, "payload": map[string]any{"station": s.station.Capabilities()}}); err != nil {
		return
	}
	for {
		select {
		case <-done:
			return
		case <-r.Context().Done():
			return
		case e, ok := <-ch:
			if !ok {
				return
			}
			if err := conn.WriteJSON(e); err != nil {
				return
			}
		}
	}
}
