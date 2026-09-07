package benchmark

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"time"
)

var errPlannedReconnect = errors.New("planned WebSocket reconnect")

type wireMessage struct {
	Type       string         `json:"type"`
	Sequence   uint64         `json:"sequence"`
	Timestamp  time.Time      `json:"timestamp"`
	CapturedAt time.Time      `json:"capturedAt"`
	Payload    map[string]any `json:"payload"`
}

func (e *runEngine) runWebSocket(ctx context.Context, index int, ready chan<- struct{}) {
	session := e.sessions[index%len(e.sessions)]
	random := rand.New(rand.NewSource(operationSeed(e.profile.Seed, "websocket", uint64(index+1))))
	firstConnection := true
	readySent := false
	awaitingResync := false
	for ctx.Err() == nil {
		started := time.Now()
		client, err := dialWebSocket(ctx, e.options.Server, session.accessToken(), e.profile.OperationTimeout.Duration)
		e.recorder.Record("websocket_connect", time.Since(started), err)
		if err != nil {
			if !waitRetry(ctx) {
				return
			}
			continue
		}
		e.wsMetrics.connections.Add(1)
		if !firstConnection {
			e.wsMetrics.reconnects.Add(1)
		}
		firstConnection = false
		closed := make(chan struct{})
		go closeWebSocketOnContext(ctx, closed, client)
		heartbeatDone := make(chan struct{})
		go e.writeWebSocketHeartbeats(ctx, heartbeatDone, client)
		err = e.consumeWebSocket(ctx, client, random, &awaitingResync, func() {
			if !readySent {
				ready <- struct{}{}
				readySent = true
			}
		})
		close(heartbeatDone)
		close(closed)
		_ = client.Close()
		e.wsMetrics.disconnections.Add(1)
		if ctx.Err() != nil {
			break
		}
		if err != nil && !errors.Is(err, errPlannedReconnect) && !errors.Is(err, io.EOF) && !isNetworkError(err) {
			e.wsMetrics.invalidMessages.Add(1)
			e.invariants.Violate(invariantValidJSON, fmt.Sprintf("WebSocket %d: %v", index, err))
		}
		if !waitRetry(ctx) {
			break
		}
	}
	if awaitingResync {
		e.invariants.Violate(invariantWebSocketResync, fmt.Sprintf("WebSocket %d ended before resynchronization", index))
	}
}

func (e *runEngine) consumeWebSocket(ctx context.Context, client *webSocketClient, random *rand.Rand, awaitingResync *bool, ready func()) error {
	var lastSequence uint64
	for {
		var message wireMessage
		if err := client.ReadJSON(&message); err != nil {
			return err
		}
		e.invariants.Observe(invariantValidJSON)
		if message.Type == "" {
			return errors.New("WebSocket message has no type")
		}
		if message.Type == "system.snapshot" {
			if err := e.monitor.processSnapshot(message.Payload); err != nil {
				return fmt.Errorf("decode system snapshot: %w", err)
			}
			e.wsMetrics.snapshots.Add(1)
			lastSequence = message.Sequence
			if *awaitingResync {
				e.invariants.Observe(invariantWebSocketResync)
				*awaitingResync = false
			}
			ready()
			continue
		}
		e.wsMetrics.eventsReceived.Add(1)
		if message.Sequence == 0 {
			return fmt.Errorf("event %s has sequence zero", message.Type)
		}
		if e.profile.Behavior.DropEventProbability > 0 && random.Float64() < e.profile.Behavior.DropEventProbability {
			continue
		}
		if message.Sequence <= lastSequence {
			continue
		}
		if !*awaitingResync && lastSequence > 0 && message.Sequence > lastSequence+1 {
			e.wsMetrics.sequenceGaps.Add(1)
			*awaitingResync = true
			if err := client.WriteJSON(map[string]any{"type": "client.snapshot_request", "lastSequence": lastSequence}, e.profile.OperationTimeout.Duration); err != nil {
				return err
			}
			e.wsMetrics.snapshotRequests.Add(1)
		}
		if !*awaitingResync && message.Sequence > lastSequence {
			lastSequence = message.Sequence
		}
		if *awaitingResync {
			continue
		}
		e.monitor.process(message.Sequence, message.Type, message.Payload)
		if e.profile.Behavior.SnapshotProbability > 0 && random.Float64() < e.profile.Behavior.SnapshotProbability {
			if err := client.WriteJSON(map[string]any{"type": "client.snapshot_request", "lastSequence": lastSequence}, e.profile.OperationTimeout.Duration); err != nil {
				return err
			}
			e.wsMetrics.snapshotRequests.Add(1)
		}
		if e.profile.Behavior.ReconnectProbability > 0 && random.Float64() < e.profile.Behavior.ReconnectProbability {
			return errPlannedReconnect
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
}

func (e *runEngine) writeWebSocketHeartbeats(ctx context.Context, done <-chan struct{}, client *webSocketClient) {
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-ticker.C:
			if err := client.WriteJSON(map[string]string{"type": "client.heartbeat"}, e.profile.OperationTimeout.Duration); err != nil {
				return
			}
		}
	}
}

func closeWebSocketOnContext(ctx context.Context, done <-chan struct{}, client *webSocketClient) {
	select {
	case <-ctx.Done():
		_ = client.Close()
	case <-done:
	}
}

func waitRetry(ctx context.Context) bool {
	timer := time.NewTimer(100 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func isNetworkError(err error) bool {
	var networkError net.Error
	return errors.As(err, &networkError)
}

func decodePayload[T any](payload map[string]any, target *T) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}
