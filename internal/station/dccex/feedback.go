package dccex

import (
	"context"

	"github.com/agm650/TrainPilot-server/internal/station"
)

func (d *Driver) FeedbackState() station.FeedbackState {
	d.mu.Lock()
	defer d.mu.Unlock()
	return station.FeedbackState{Source: "dccex", Generation: d.feedbackGeneration, PowerGeneration: d.feedbackPowerGeneration,
		Available: !d.closed && d.health.Health().Connectivity == station.Online}
}

func (d *Driver) BeginFeedbackPowerSync() {
	d.writeMu.Lock()
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		d.writeMu.Unlock()
		return
	}
	c := d.conn
	d.conn = nil
	d.connectionID++ // Old TCP readers must not enter the new power stream.
	d.feedbackPowerGeneration++
	d.mu.Unlock()
	if c != nil {
		_ = c.Close()
	}
	d.writeMu.Unlock()
}

func (d *Driver) RefreshFeedback(ctx context.Context) error {
	d.writeMu.Lock()
	d.mu.Lock()
	if d.closed || d.reconnecting {
		d.mu.Unlock()
		d.writeMu.Unlock()
		return station.ErrOffline
	}
	connected := d.conn != nil
	d.mu.Unlock()
	if !connected {
		connection, err := d.dial(ctx, d.address)
		if err != nil {
			d.mu.Lock()
			start := !d.closed && !d.reconnecting
			if start {
				d.reconnecting = true
				d.wg.Add(1)
			}
			d.mu.Unlock()
			d.health.CommunicationError()
			d.writeMu.Unlock()
			d.publishHealthStatus()
			if start {
				go d.reconnectLoop()
			}
			return err
		}
		d.mu.Lock()
		if d.closed || d.reconnecting {
			d.mu.Unlock()
			_ = connection.Close()
			d.writeMu.Unlock()
			return station.ErrOffline
		}
		d.conn = connection
		d.connectionID++
		id := d.connectionID
		d.wg.Add(1)
		d.mu.Unlock()
		go d.readLoop(connection, id)
	}
	d.writeMu.Unlock()
	// Official DCC-EX query: current status of all defined sensors. It never
	// replays a locomotive or accessory command.
	return d.send(ctx, "<Q>\n")
}
