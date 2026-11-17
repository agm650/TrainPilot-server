package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/station"
)

func TestTrackPowerInvalidatesDependentSourcesOnlyAndRequiresFreshData(t *testing.T) {
	ctx := context.Background()
	dependent, independent := true, false
	current := occupancyProvider("current", 100, true, 0)
	current.TrackPowerDependent = &dependent
	camera := occupancyProvider("camera", 100, true, 0)
	camera.TrackPowerDependent = &independent
	f := newOccupancyTestFixture(t, []model.OccupancyProvider{current, camera}, []model.OccupancySensorMapping{
		occupancyMapping("current", "1", "block-a"), occupancyMapping("camera", "2", "block-b"),
	})
	observe := func(provider, sensor string, state model.OccupancyState, sequence uint64) {
		t.Helper()
		if err := f.service.Observe(ctx, model.OccupancyObservation{ProviderID: provider, SensorID: sensor, State: state, Sequence: sequence, ObservedAt: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
	}
	observe("current", "1", model.OccupancyFree, 1)
	observe("camera", "2", model.OccupancyOccupied, 1)
	if f.service.BlockState("block-a").State != model.OccupancyUnknown || f.service.BlockState("block-b").State != model.OccupancyOccupied {
		t.Fatal("unknown power accepted current-based free or invalidated independent occupancy")
	}
	if err := f.service.SetTrackPowerAvailable(ctx, true); err != nil {
		t.Fatal(err)
	}
	if f.service.BlockState("block-a").State != model.OccupancyUnknown {
		t.Fatal("ON revived an OFF observation")
	}
	observe("current", "1", model.OccupancyOccupied, 2)
	if err := f.service.SetTrackPowerAvailable(ctx, false); err != nil {
		t.Fatal(err)
	}
	observe("current", "1", model.OccupancyFree, 3)
	sources, err := f.service.ProviderStates(ctx, "block-a")
	if err != nil || len(sources) != 1 || sources[0].Available || !sources[0].TrackPowerDependent {
		t.Fatalf("power diagnostics=%+v err=%v", sources, err)
	}
	if f.service.BlockState("block-a").State != model.OccupancyUnknown || f.service.BlockState("block-b").State != model.OccupancyOccupied {
		t.Fatal("OFF did not preserve source independence")
	}
	if err := f.service.SetTrackPowerAvailable(ctx, true); err != nil {
		t.Fatal(err)
	}
	if f.service.BlockState("block-a").State != model.OccupancyUnknown {
		t.Fatal("OFF feedback was reused after ON")
	}
	observe("current", "1", model.OccupancyOccupied, 4)
	if f.service.BlockState("block-a").State != model.OccupancyOccupied {
		t.Fatal("fresh observation did not restore occupancy")
	}
}

func TestPowerEpochPreservesIndependentWildcardAndRejectsQueuedOldFrame(t *testing.T) {
	ctx := context.Background()
	independent := false
	rbus := occupancyProvider("z21-rbus", 100, true, 0)
	wildcard := occupancyProvider("*", 100, true, 0)
	wildcard.TrackPowerDependent = &independent
	f := newOccupancyTestFixture(t, []model.OccupancyProvider{rbus, wildcard}, []model.OccupancySensorMapping{
		occupancyMapping("z21-rbus", "1", "block-a"), occupancyMapping("*", "81", "block-b"),
	})
	if err := f.service.SetTrackPowerAvailable(ctx, true); err != nil {
		t.Fatal(err)
	}
	provider := &feedbackStateStub{state: station.FeedbackState{Source: "z21-rbus", Generation: 1, Available: true}}
	observations := []model.OccupancyObservation{
		{ProviderID: "z21-rbus", SensorID: "1", State: model.OccupancyOccupied, Sequence: 1, ObservedAt: time.Now().UTC()},
		{ProviderID: "*", SensorID: "81", State: model.OccupancyOccupied, Sequence: 1, ObservedAt: time.Now().UTC()},
	}
	if err := f.service.ObserveStationBatch(ctx, observations, provider, 1, 0); err != nil {
		t.Fatal(err)
	}
	if err := f.service.SetTrackPowerAvailable(ctx, false); err != nil {
		t.Fatal(err)
	}
	provider.set(station.FeedbackState{Source: "z21-rbus", Generation: 1, PowerGeneration: 1, Available: true})
	if err := f.service.SyncStationFeedback(ctx, provider); err != nil {
		t.Fatal(err)
	}
	if err := f.service.SetTrackPowerAvailable(ctx, true); err != nil {
		t.Fatal(err)
	}
	if err := f.service.ObserveStationBatch(ctx, observations, provider, 1, 0); !errors.Is(err, ErrFeedbackGenerationStale) {
		t.Fatalf("old power generation accepted: %v", err)
	}
	if f.service.BlockState("block-a").State != model.OccupancyUnknown || f.service.BlockState("block-b").State != model.OccupancyOccupied {
		t.Fatal("power generation invalidated independent source or revived old dependent data")
	}
}

func TestOccupancyPowerFaultsDoNotConfuseEmergencyStopWithPowerLoss(t *testing.T) {
	for _, condition := range []string{"off", "unknown", "short", "internal_short", "power_lost", "emergency_stop"} {
		t.Run(condition, func(t *testing.T) {
			ctx := context.Background()
			c, db, _, _, _, _ := newControlFixture(t)
			if err := db.SetFeedbackMapping(ctx, "z21-rbus", 1, "block-a"); err != nil {
				t.Fatal(err)
			}
			r := NewRailwayService(db, c.station, c.events)
			c.SetOccupancyProvider(r.occupancy, "z21-rbus")
			c.updateOccupancyTrackPower(ctx, station.Status{TrackPower: "on"})
			if err := r.occupancy.Observe(ctx, model.OccupancyObservation{ProviderID: "z21-rbus", SensorID: "1", State: model.OccupancyOccupied, Sequence: 1, ObservedAt: time.Now().UTC()}); err != nil {
				t.Fatal(err)
			}
			status := station.Status{TrackPower: "on", Connectivity: station.Online}
			switch condition {
			case "off":
				status.TrackPower = "off"
			case "unknown":
				status.TrackPower = "unknown"
			case "short":
				status.ShortCircuit = true
			case "internal_short":
				status.InternalShortCircuit = true
			case "power_lost":
				status.PowerLost = true
			case "emergency_stop":
				status.EmergencyStop = true
			}
			c.publishStationStatusChanges(status)
			want := model.OccupancyUnknown
			if condition == "emergency_stop" {
				want = model.OccupancyOccupied
			}
			if got := r.occupancy.BlockState("block-a").State; got != want {
				t.Fatalf("condition=%s state=%s want=%s", condition, got, want)
			}
		})
	}
}
