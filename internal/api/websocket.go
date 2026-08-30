package api

import (
	"context"
	"net/http"
	"time"

	"github.com/agm650/TrainPilot-server/internal/events"
	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/station"
	ws "github.com/agm650/TrainPilot-server/internal/websocket"
)

type systemSnapshotPayload struct {
	Station                 station.Capabilities           `json:"station"`
	StationStatus           station.Status                 `json:"stationStatus"`
	Locomotives             []model.Locomotive             `json:"locomotives"`
	ControlLeases           []model.ControlLease           `json:"controlLeases"`
	LocomotiveControlStates []model.LocomotiveControlState `json:"locomotiveControlStates"`
	Blocks                  []model.Block                  `json:"blocks"`
	Turnouts                []model.Turnout                `json:"turnouts"`
	Routes                  []model.Route                  `json:"routes"`
}

type systemSnapshot struct {
	Type       string                `json:"type"`
	Sequence   uint64                `json:"sequence"`
	CapturedAt time.Time             `json:"capturedAt"`
	Payload    systemSnapshotPayload `json:"payload"`
}

func snapshotItems[T any](items []T) []T {
	if items == nil {
		return []T{}
	}
	return items
}

func (s *Server) buildSystemSnapshot(ctx context.Context, session model.Session) (systemSnapshot, error) {
	// Capture the sequence before reading the current state. Events produced
	// while the snapshot is being built will therefore have a greater sequence.
	// Clients must ignore queued events whose sequence is not greater than the
	// snapshot sequence, because the snapshot already supersedes them.
	sequence := s.events.CurrentSequence()

	status, err := s.control.StationStatus(ctx)
	if err != nil {
		return systemSnapshot{}, err
	}
	locomotives, err := s.railway.Locomotives(ctx)
	if err != nil {
		return systemSnapshot{}, err
	}
	leases, err := s.control.LeasesForSession(ctx, session)
	if err != nil {
		return systemSnapshot{}, err
	}
	controlStates, err := s.control.LocomotiveControlStates(ctx, session)
	if err != nil {
		return systemSnapshot{}, err
	}
	blocks, err := s.railway.Blocks(ctx)
	if err != nil {
		return systemSnapshot{}, err
	}
	turnouts, err := s.railway.Turnouts(ctx)
	if err != nil {
		return systemSnapshot{}, err
	}
	routes, err := s.routes.List(ctx)
	if err != nil {
		return systemSnapshot{}, err
	}
	return systemSnapshot{
		Type:       "system.snapshot",
		Sequence:   sequence,
		CapturedAt: time.Now().UTC(),
		Payload: systemSnapshotPayload{
			Station:                 s.station.Capabilities(),
			StationStatus:           status,
			Locomotives:             snapshotItems(locomotives),
			ControlLeases:           snapshotItems(leases),
			LocomotiveControlStates: snapshotItems(controlStates),
			Blocks:                  snapshotItems(blocks),
			Turnouts:                snapshotItems(turnouts),
			Routes:                  snapshotItems(routes),
		},
	}, nil
}

func (s *Server) writeSystemSnapshot(conn *ws.Conn, r *http.Request) (uint64, error) {
	snapshot, err := s.buildSystemSnapshot(r.Context(), sessionFrom(r))
	if err != nil {
		return 0, err
	}
	if err := s.writeWebSocketJSON(conn, snapshot); err != nil {
		return 0, err
	}
	return snapshot.Sequence, nil
}

func (s *Server) writeWebSocketJSON(conn *ws.Conn, value any) error {
	timeout := s.eventWriteTimeout
	if timeout <= 0 {
		timeout = defaultEventWriteTimeout
	}
	if err := conn.SetWriteDeadline(time.Now().Add(timeout)); err != nil {
		return err
	}
	err := conn.WriteJSON(value)
	if err == nil {
		_ = conn.SetWriteDeadline(time.Time{})
	}
	return err
}

func eventFollowsSequence(event events.Event, sequence uint64) bool {
	return event.Sequence > sequence
}

func (s *Server) eventsWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := ws.Accept(w, r)
	if err != nil {
		return
	}
	defer conn.Close()

	bufferSize := s.eventBuffer
	if bufferSize <= 0 {
		bufferSize = defaultEventBufferSize
	}
	ch, overflow, unsubscribe := s.events.SubscribeWithOverflow(bufferSize)
	defer unsubscribe()

	done := make(chan struct{})
	snapshotRequests := make(chan struct{}, 1)
	sessionExpiry := time.NewTimer(time.Until(sessionFrom(r).AccessExpiry))
	defer sessionExpiry.Stop()
	sessionCheck := time.NewTicker(time.Second)
	defer sessionCheck.Stop()

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
				// Heartbeats update session observability but extend neither the
				// access token nor a lease. They consume no event sequence.
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

	lastSequence, err := s.writeSystemSnapshot(conn, r)
	if err != nil {
		return
	}

	for {
		select {
		case <-done:
			return
		case <-r.Context().Done():
			return
		case <-sessionExpiry.C:
			return
		case <-sessionCheck.C:
			session, err := s.store.SessionByID(r.Context(), sessionFrom(r).ID)
			if err != nil || session.RevokedAt != nil {
				return
			}
		case <-overflow:
			// At least one sequence was lost for this subscriber. Closing the
			// connection forces a complete snapshot on reconnect and prevents a
			// client from continuing with a silently incomplete state.
			return
		case <-snapshotRequests:
			sequence, err := s.writeSystemSnapshot(conn, r)
			if err != nil {
				return
			}
			lastSequence = sequence
		case e, ok := <-ch:
			if !ok {
				return
			}
			if !eventFollowsSequence(e, lastSequence) {
				continue
			}
			if err := s.writeWebSocketJSON(conn, e); err != nil {
				return
			}
			lastSequence = e.Sequence
		}
	}
}
