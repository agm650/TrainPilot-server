package api

import (
	"net/http"
	"time"

	ws "github.com/agm650/TrainPilot-server/internal/websocket"
)

func (s *Server) writeSystemSnapshot(conn *ws.Conn, r *http.Request) error {
	// Capture the sequence before reading the current state. Events produced
	// while the snapshot is being built will therefore have a greater sequence
	// number and remain eligible for processing by the client.
	sequence := s.events.CurrentSequence()

	status, err := s.control.StationStatus(r.Context())
	if err != nil {
		return err
	}

	return conn.WriteJSON(map[string]any{
		"type":     "system.snapshot",
		"sequence": sequence,
		"payload": map[string]any{
			"station":       s.station.Capabilities(),
			"stationStatus": status,
		},
	})
}

func (s *Server) eventsWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := ws.Accept(w, r)
	if err != nil {
		return
	}
	defer conn.Close()

	ch, unsubscribe := s.events.Subscribe(64)
	defer unsubscribe()

	done := make(chan struct{})
	snapshotRequests := make(chan struct{}, 1)

	go func() {
		defer close(done)
		for {
			var incoming struct {
				Type         string `json:"type"`
				LastSequence uint64 `json:"lastSequence,omitempty"`
			}
			if err := conn.ReadJSON(&incoming); err != nil {
				return
			}

			switch incoming.Type {
			case "client.heartbeat":
				// Heartbeats keep the authenticated session alive but are not
				// server events. They must not consume a global event sequence.
				_ = s.store.TouchSession(r.Context(), sessionFrom(r).ID, time.Now().UTC())

			case "client.snapshot_request":
				// Coalesce repeated requests. The writer loop is the only code
				// writing to the WebSocket connection.
				select {
				case snapshotRequests <- struct{}{}:
				default:
				}
			}
		}
	}()

	if err := s.writeSystemSnapshot(conn, r); err != nil {
		return
	}

	for {
		select {
		case <-done:
			return
		case <-r.Context().Done():
			return
		case <-snapshotRequests:
			if err := s.writeSystemSnapshot(conn, r); err != nil {
				return
			}
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
