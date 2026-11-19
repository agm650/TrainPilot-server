package z21

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/station"
)

type statusProbeResult struct {
	status station.Status
	err    error
}

func startStatusProbe(ctx context.Context, d *Driver) <-chan statusProbeResult {
	result := make(chan statusProbeResult, 1)
	go func() { status, err := d.Status(ctx); result <- statusProbeResult{status, err} }()
	return result
}

func awaitStatusProbe(t *testing.T, result <-chan statusProbeResult) statusProbeResult {
	t.Helper()
	select {
	case got := <-result:
		return got
	case <-time.After(3 * time.Second):
		t.Fatal("status probe blocked")
		return statusProbeResult{}
	}
}

func TestSystemStatusDoesNotRequireSecondaryXReply(t *testing.T) {
	for _, secondary := range []bool{true, false} {
		t.Run(map[bool]string{true: "both", false: "system_only"}[secondary], func(t *testing.T) {
			server := newUDPServer(t)
			d := newConnectedTestDriver(t, server, time.Millisecond)
			d.started = true
			before := d.FeedbackState()
			for range 3 {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				result := startStatusProbe(ctx, d)
				x, system := server.next(t), server.next(t)
				if x.data[4] != 0x21 || system.data[2] != 0x85 {
					cancel()
					t.Fatal("missing status queries", x, system)
				}
				if secondary {
					server.send(t, x.from, xbus(0x62, 0x22, 0))
				}
				payload := make([]byte, 16)
				payload[12], payload[13] = 0x05, 0x0c // EStop, short circuit, external/internal faults.
				server.send(t, system.from, lan(0x0084, payload...))
				got := awaitStatusProbe(t, result)
				cancel()
				if got.err != nil || got.status.Connectivity != station.Online || got.status.TrackPower != "on" || !got.status.EmergencyStop || !got.status.ShortCircuit || !got.status.ExternalShortCircuit || !got.status.InternalShortCircuit {
					t.Fatal(got)
				}
				if after := d.FeedbackState(); after.Generation != before.Generation || after.PowerGeneration != before.PowerGeneration {
					t.Fatal("valid system reply restarted feedback", before, after)
				}
			}
			d.statusMu.Lock()
			waiters := len(d.xStatusWaiters) + len(d.systemStateWaiters)
			d.statusMu.Unlock()
			if waiters != 0 {
				t.Fatal("status waiters leaked", waiters)
			}
		})
	}
}

func TestXOnlyStatusKeepsTransportButDoesNotReuseCachedPower(t *testing.T) {
	server := newUDPServer(t)
	d := newConnectedTestDriver(t, server, time.Millisecond)
	d.started = true
	d.parse(lan(0x0084, make([]byte, 16)...))
	before := d.FeedbackState()
	if d.currentStatus().TrackPower != "on" {
		t.Fatal("fixture did not cache power")
	}
	result := startStatusProbe(context.Background(), d)
	x := server.next(t)
	server.next(t) // Deliberately leave system status unanswered.
	server.send(t, x.from, xbus(0x62, 0x22, 0))
	got := awaitStatusProbe(t, result)
	if got.err != nil || got.status.Connectivity != station.Online || got.status.TrackPower != "unknown" {
		t.Fatal(got)
	}
	if after := d.FeedbackState(); after.Generation != before.Generation || !after.Available {
		t.Fatal("partial status restarted transport", before, after)
	}
}

func TestRBusDataAloneDoesNotSatisfyStatusProbe(t *testing.T) {
	server := newUDPServer(t)
	d := newConnectedTestDriver(t, server, time.Millisecond)
	d.started = true
	before := d.FeedbackState()
	result := startStatusProbe(context.Background(), d)
	x := server.next(t)
	server.next(t)
	payload := make([]byte, 11)
	payload[1] = 2
	server.send(t, x.from, lan(0x0080, payload...))
	got := awaitStatusProbe(t, result)
	if got.err != nil || got.status.Connectivity != station.Degraded || got.status.TrackPower != "unknown" {
		t.Fatal(got)
	}
	if after := d.FeedbackState(); after.Generation <= before.Generation || after.Available {
		t.Fatal("no-status timeout kept occupancy available", before, after)
	}
}

func TestConcurrentStatusCallersShareFreshSystemReply(t *testing.T) {
	server := newUDPServer(t)
	d := newConnectedTestDriver(t, server, time.Millisecond)
	d.started = true
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	before := d.FeedbackState()
	results := make([]<-chan statusProbeResult, 8)
	for i := range results {
		results[i] = startStatusProbe(ctx, d)
	}
	var packet udpDatagram
	for range 2 * len(results) {
		packet = server.next(t)
	}
	server.send(t, packet.from, lan(0x0084, make([]byte, 16)...))
	for _, result := range results {
		got := awaitStatusProbe(t, result)
		if got.err != nil || got.status.TrackPower != "on" || got.status.Connectivity != station.Online {
			t.Fatal(got)
		}
	}
	if after := d.FeedbackState(); after.Generation != before.Generation {
		t.Fatal("concurrent callers restarted transport", before, after)
	}
}

func TestStatusFollowsPowerEpochAndRejectsRetiredCallbacks(t *testing.T) {
	server := newUDPServer(t)
	d := newConnectedTestDriver(t, server, time.Millisecond)
	d.started = true
	d.parse(lan(0x0084, make([]byte, 16)...))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	before := d.FeedbackState()
	d.mu.Lock()
	old := d.conn
	d.mu.Unlock()
	result := startStatusProbe(ctx, d)
	server.next(t)
	server.next(t)
	d.BeginFeedbackPowerSync()
	if got := d.currentStatus(); got.TrackPower != "unknown" {
		t.Fatal("power epoch retained old status", got)
	}
	if err := d.RefreshFeedback(ctx); err != nil {
		t.Fatal(err)
	}
	var system udpDatagram
	for i := 0; i < 12; i++ {
		packet := server.next(t)
		if packet.data[2] == 0x85 {
			system = packet
			break
		}
	}
	if system.from == nil {
		t.Fatal("new epoch did not query system status")
	}
	d.publishXStatus(0, old, before.Generation, before.PowerGeneration)
	d.publishSystemState(systemState{}, old, before.Generation, before.PowerGeneration)
	select {
	case got := <-result:
		t.Fatal("retired status completed new probe", got)
	default:
	}
	server.send(t, system.from, lan(0x0084, make([]byte, 16)...))
	got := awaitStatusProbe(t, result)
	if got.err != nil || got.status.TrackPower != "on" || got.status.Connectivity != station.Online {
		t.Fatal(got)
	}
	if after := d.FeedbackState(); after.Generation != before.Generation || after.PowerGeneration != before.PowerGeneration+1 {
		t.Fatal("power epoch caused network reset", before, after)
	}
}

func TestStatusCancellationAndCloseReleaseWaiters(t *testing.T) {
	for _, closeDriver := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "close"}[closeDriver], func(t *testing.T) {
			server := newUDPServer(t)
			d := newConnectedTestDriver(t, server, time.Millisecond)
			d.started = true
			before := d.FeedbackState()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result := startStatusProbe(ctx, d)
			server.next(t)
			server.next(t)
			want := context.Canceled
			if closeDriver {
				_ = d.Close()
				want = station.ErrOffline
			} else {
				cancel()
			}
			got := awaitStatusProbe(t, result)
			if !errors.Is(got.err, want) {
				t.Fatal(got.err, want)
			}
			if after := d.FeedbackState(); after.Generation != before.Generation {
				t.Fatal("caller cancellation restarted transport", before, after)
			}
			d.statusMu.Lock()
			waiters := len(d.xStatusWaiters) + len(d.systemStateWaiters)
			d.statusMu.Unlock()
			if waiters != 0 {
				t.Fatal("status waiter leak", waiters)
			}
		})
	}
}
