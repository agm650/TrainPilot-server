package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/clock"
	"github.com/agm650/TrainPilot-server/internal/events"
	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/store"
)

type occupancyTestFixture struct {
	store   *store.Store
	service *OccupancyService
	clock   *clock.Fake
	bus     *events.Bus
}

func newOccupancyTestFixture(t *testing.T, providers []model.OccupancyProvider, mappings []model.OccupancySensorMapping) *occupancyTestFixture {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	blocks := make(map[string]bool)
	var definitions []model.BlockDefinition
	for _, mapping := range mappings {
		if !blocks[mapping.BlockID] {
			blocks[mapping.BlockID] = true
			definitions = append(definitions, model.BlockDefinition{ID: mapping.BlockID, Name: mapping.BlockID})
		}
	}
	if err := db.ImportLayout(ctx, model.LayoutDefinition{Blocks: definitions}, false); err != nil {
		t.Fatal(err)
	}
	for _, provider := range providers {
		if err := db.SetOccupancyProvider(ctx, provider); err != nil {
			t.Fatal(err)
		}
	}
	for _, mapping := range mappings {
		if err := db.SetOccupancySensorMapping(ctx, mapping); err != nil {
			t.Fatal(err)
		}
	}
	fakeClock := clock.NewFake(time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC))
	bus := events.New()
	return &occupancyTestFixture{store: db, service: NewOccupancyService(db, bus, fakeClock), clock: fakeClock, bus: bus}
}

func occupancyProvider(id string, priority int, required bool, staleAfter time.Duration) model.OccupancyProvider {
	return model.OccupancyProvider{
		ID: id, Type: "test", Priority: priority, Required: required,
		StaleAfter: staleAfter, FreshnessRequired: staleAfter > 0,
	}
}

func occupancyMapping(providerID, sensorID, blockID string) model.OccupancySensorMapping {
	return model.OccupancySensorMapping{ProviderID: providerID, SensorID: sensorID, BlockID: blockID}
}

func observeOccupancy(t *testing.T, service *OccupancyService, providerID, sensorID string, state model.OccupancyState, sequence uint64, occupant *model.OccupantRef) {
	t.Helper()
	err := service.Observe(context.Background(), model.OccupancyObservation{
		ProviderID: providerID, SensorID: sensorID, State: state, Sequence: sequence,
		ObservedAt: time.Date(2026, 9, 19, 12, 0, int(sequence), 0, time.UTC), Occupant: occupant,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestOccupancyAggregationSafetyRules(t *testing.T) {
	tests := []struct {
		name      string
		providers []model.OccupancyProvider
		mappings  []model.OccupancySensorMapping
		observe   func(*testing.T, *occupancyTestFixture)
		want      model.OccupancyState
	}{
		{
			name:      "one optional free",
			providers: []model.OccupancyProvider{occupancyProvider("optional", 10, false, time.Minute)},
			mappings:  []model.OccupancySensorMapping{occupancyMapping("optional", "1", "block")},
			observe: func(t *testing.T, f *occupancyTestFixture) {
				observeOccupancy(t, f.service, "optional", "1", model.OccupancyFree, 1, nil)
			},
			want: model.OccupancyFree,
		},
		{
			name:      "one occupied",
			providers: []model.OccupancyProvider{occupancyProvider("required", 10, true, time.Minute)},
			mappings:  []model.OccupancySensorMapping{occupancyMapping("required", "1", "block")},
			observe: func(t *testing.T, f *occupancyTestFixture) {
				observeOccupancy(t, f.service, "required", "1", model.OccupancyOccupied, 1, nil)
			},
			want: model.OccupancyOccupied,
		},
		{
			name: "low priority occupied wins",
			providers: []model.OccupancyProvider{
				occupancyProvider("low", 10, false, time.Minute),
				occupancyProvider("high", 100, false, time.Minute),
			},
			mappings: []model.OccupancySensorMapping{
				occupancyMapping("low", "1", "block"), occupancyMapping("high", "1", "block"),
			},
			observe: func(t *testing.T, f *occupancyTestFixture) {
				observeOccupancy(t, f.service, "high", "1", model.OccupancyFree, 1, nil)
				observeOccupancy(t, f.service, "low", "1", model.OccupancyOccupied, 1, nil)
			},
			want: model.OccupancyOccupied,
		},
		{
			name: "all required free",
			providers: []model.OccupancyProvider{
				occupancyProvider("a", 10, true, time.Minute), occupancyProvider("b", 20, true, time.Minute),
			},
			mappings: []model.OccupancySensorMapping{
				occupancyMapping("a", "1", "block"), occupancyMapping("b", "1", "block"),
			},
			observe: func(t *testing.T, f *occupancyTestFixture) {
				observeOccupancy(t, f.service, "a", "1", model.OccupancyFree, 1, nil)
				observeOccupancy(t, f.service, "b", "1", model.OccupancyFree, 1, nil)
			},
			want: model.OccupancyFree,
		},
		{
			name:      "no fresh source",
			providers: []model.OccupancyProvider{occupancyProvider("optional", 10, false, time.Minute)},
			mappings:  []model.OccupancySensorMapping{occupancyMapping("optional", "1", "block")},
			observe:   func(t *testing.T, f *occupancyTestFixture) {},
			want:      model.OccupancyUnknown,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newOccupancyTestFixture(t, test.providers, test.mappings)
			test.observe(t, fixture)
			if got := fixture.service.BlockState("block").State; got != test.want {
				t.Fatalf("state = %q, want %q", got, test.want)
			}
		})
	}
}

func TestOccupancyFreshnessRequiredAndOptional(t *testing.T) {
	providers := []model.OccupancyProvider{
		occupancyProvider("required", 100, true, time.Minute),
		occupancyProvider("optional", 10, false, time.Minute),
	}
	mappings := []model.OccupancySensorMapping{
		occupancyMapping("required", "1", "block"), occupancyMapping("optional", "1", "block"),
	}
	t.Run("required stale optional free", func(t *testing.T) {
		fixture := newOccupancyTestFixture(t, providers, mappings)
		observeOccupancy(t, fixture.service, "required", "1", model.OccupancyFree, 1, nil)
		fixture.clock.Advance(2 * time.Minute)
		observeOccupancy(t, fixture.service, "optional", "1", model.OccupancyFree, 1, nil)
		if got := fixture.service.BlockState("block").State; got != model.OccupancyUnknown {
			t.Fatalf("state = %q", got)
		}
	})
	t.Run("required stale optional occupied", func(t *testing.T) {
		fixture := newOccupancyTestFixture(t, providers, mappings)
		observeOccupancy(t, fixture.service, "required", "1", model.OccupancyFree, 1, nil)
		fixture.clock.Advance(2 * time.Minute)
		observeOccupancy(t, fixture.service, "optional", "1", model.OccupancyOccupied, 1, nil)
		if got := fixture.service.BlockState("block").State; got != model.OccupancyOccupied {
			t.Fatalf("state = %q", got)
		}
	})
	t.Run("optional stale required free", func(t *testing.T) {
		fixture := newOccupancyTestFixture(t, providers, mappings)
		observeOccupancy(t, fixture.service, "optional", "1", model.OccupancyFree, 1, nil)
		fixture.clock.Advance(2 * time.Minute)
		observeOccupancy(t, fixture.service, "required", "1", model.OccupancyFree, 1, nil)
		if got := fixture.service.BlockState("block").State; got != model.OccupancyFree {
			t.Fatalf("state = %q", got)
		}
	})
}

func TestOccupancyPriorityChoosesOccupantAndDetectsTie(t *testing.T) {
	providers := []model.OccupancyProvider{
		occupancyProvider("low", 10, false, time.Minute),
		occupancyProvider("high", 90, false, time.Minute),
	}
	mappings := []model.OccupancySensorMapping{
		occupancyMapping("low", "1", "block"), occupancyMapping("high", "1", "block"),
	}
	fixture := newOccupancyTestFixture(t, providers, mappings)
	observeOccupancy(t, fixture.service, "low", "1", model.OccupancyOccupied, 1, &model.OccupantRef{Type: "locomotive", ID: "low"})
	observeOccupancy(t, fixture.service, "high", "1", model.OccupancyOccupied, 1, &model.OccupantRef{Type: "locomotive", ID: "high"})
	if got := fixture.service.BlockState("block").Occupant; got == nil || got.ID != "high" {
		t.Fatalf("occupant = %+v", got)
	}

	priority := 90
	mapping, err := fixture.store.OccupancySensorMapping(context.Background(), "low", "1")
	if err != nil {
		t.Fatal(err)
	}
	mapping.Priority = &priority
	if err := fixture.store.SetOccupancySensorMapping(context.Background(), mapping); err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.Recompute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := fixture.service.BlockState("block").Occupant; got != nil {
		t.Fatalf("conflicting equal-priority occupant = %+v", got)
	}
}

func TestOccupancySequenceAndRefresh(t *testing.T) {
	fixture := newOccupancyTestFixture(t,
		[]model.OccupancyProvider{occupancyProvider("provider", 10, true, time.Minute)},
		[]model.OccupancySensorMapping{occupancyMapping("provider", "1", "block")},
	)
	ctx := context.Background()
	first := model.OccupancyObservation{
		ProviderID: "provider", SensorID: "1", State: model.OccupancyOccupied, Sequence: 100,
		ObservedAt: fixture.clock.Now(),
	}
	if err := fixture.service.Observe(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.Observe(ctx, first); err != nil {
		t.Fatalf("identical duplicate: %v", err)
	}
	conflict := first
	conflict.State = model.OccupancyFree
	if err := fixture.service.Observe(ctx, conflict); !errors.Is(err, ErrOccupancySequenceConflict) {
		t.Fatalf("conflicting duplicate error = %v", err)
	}
	stale := first
	stale.Sequence = 99
	if err := fixture.service.Observe(ctx, stale); !errors.Is(err, ErrOccupancySequenceStale) {
		t.Fatalf("stale error = %v", err)
	}
	if got := fixture.service.BlockState("block").State; got != model.OccupancyOccupied {
		t.Fatalf("state after rejected observations = %q", got)
	}

	fixture.clock.Advance(50 * time.Second)
	refresh := first
	refresh.Sequence = 101
	refresh.ObservedAt = fixture.clock.Now()
	if err := fixture.service.Observe(ctx, refresh); err != nil {
		t.Fatal(err)
	}
	fixture.clock.Advance(50 * time.Second)
	if err := fixture.service.Recompute(ctx); err != nil {
		t.Fatal(err)
	}
	if got := fixture.service.BlockState("block").State; got != model.OccupancyOccupied {
		t.Fatalf("refreshed state = %q", got)
	}
	fixture.clock.Advance(11 * time.Second)
	if err := fixture.service.Recompute(ctx); err != nil {
		t.Fatal(err)
	}
	if got := fixture.service.BlockState("block").State; got != model.OccupancyUnknown {
		t.Fatalf("expired state = %q", got)
	}
}

func TestOccupancyPublishesOnlyStateChanges(t *testing.T) {
	fixture := newOccupancyTestFixture(t,
		[]model.OccupancyProvider{occupancyProvider("provider", 10, true, time.Minute)},
		[]model.OccupancySensorMapping{occupancyMapping("provider", "1", "block")},
	)
	eventChannel, unsubscribe := fixture.bus.Subscribe(4)
	defer unsubscribe()
	observeOccupancy(t, fixture.service, "provider", "1", model.OccupancyFree, 1, nil)
	event := <-eventChannel
	if event.Type != "block.occupancy.changed" {
		t.Fatalf("event type = %q", event.Type)
	}
	fixture.clock.Advance(10 * time.Second)
	observeOccupancy(t, fixture.service, "provider", "1", model.OccupancyFree, 2, nil)
	select {
	case event := <-eventChannel:
		t.Fatalf("unchanged refresh published %+v", event)
	default:
	}
}

func TestOccupancyRunExpiresWithoutObservation(t *testing.T) {
	fixture := newOccupancyTestFixture(t,
		[]model.OccupancyProvider{occupancyProvider("provider", 10, true, time.Minute)},
		[]model.OccupancySensorMapping{occupancyMapping("provider", "1", "block")},
	)
	observeOccupancy(t, fixture.service, "provider", "1", model.OccupancyFree, 1, nil)
	eventChannel, unsubscribe := fixture.bus.Subscribe(4)
	defer unsubscribe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- fixture.service.Run(ctx, time.Millisecond) }()
	fixture.clock.Advance(2 * time.Minute)
	select {
	case event := <-eventChannel:
		occupancy, ok := event.Payload.(model.BlockOccupancyChanged)
		if !ok || occupancy.State != model.OccupancyUnknown {
			t.Fatalf("expiration event = %#v", event.Payload)
		}
	case <-time.After(time.Second):
		t.Fatal("expiration event not published")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v", err)
	}
}

func TestOccupancyConcurrentProviders(t *testing.T) {
	const count = 100
	providers := make([]model.OccupancyProvider, 0, count)
	mappings := make([]model.OccupancySensorMapping, 0, count)
	for index := 0; index < count; index++ {
		id := fmt.Sprintf("provider-%03d", index)
		providers = append(providers, occupancyProvider(id, index%101, false, time.Minute))
		mappings = append(mappings, occupancyMapping(id, "sensor", "block"))
	}
	fixture := newOccupancyTestFixture(t, providers, mappings)
	var wait sync.WaitGroup
	errorsChannel := make(chan error, count)
	for index := 0; index < count; index++ {
		index := index
		wait.Add(1)
		go func() {
			defer wait.Done()
			state := model.OccupancyFree
			if index == count-1 {
				state = model.OccupancyOccupied
			}
			errorsChannel <- fixture.service.Observe(context.Background(), model.OccupancyObservation{
				ProviderID: fmt.Sprintf("provider-%03d", index), SensorID: "sensor", State: state, Sequence: 1,
			})
		}()
	}
	wait.Wait()
	close(errorsChannel)
	for err := range errorsChannel {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := fixture.service.BlockState("block").State; got != model.OccupancyOccupied {
		t.Fatalf("state = %q", got)
	}
	states, err := fixture.service.ProviderStates(context.Background(), "block")
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != count {
		t.Fatalf("provider states = %d", len(states))
	}
}

func TestOccupancyConcurrentSnapshotAndFeedbackAcrossBlocks(t *testing.T) {
	fixture := newOccupancyTestFixture(t,
		[]model.OccupancyProvider{
			occupancyProvider("rbus", 100, true, 0),
			occupancyProvider("camera", 10, false, time.Minute),
		},
		[]model.OccupancySensorMapping{
			occupancyMapping("rbus", "1", "block-a"),
			occupancyMapping("rbus", "2", "block-b"),
			occupancyMapping("camera", "a", "block-a"),
			occupancyMapping("camera", "b", "block-b"),
		},
	)
	const observations = 100
	errorsChannel := make(chan error, 2)
	go func() {
		for sequence := uint64(1); sequence <= observations; sequence++ {
			for _, sensorID := range []string{"1", "2"} {
				if err := fixture.service.Observe(context.Background(), model.OccupancyObservation{
					ProviderID: "rbus", SensorID: sensorID, State: model.OccupancyFree, Sequence: sequence,
				}); err != nil {
					errorsChannel <- err
					return
				}
			}
		}
		errorsChannel <- nil
	}()
	go func() {
		for sequence := uint64(1); sequence <= observations; sequence++ {
			if err := fixture.service.ObserveBatch(context.Background(), []model.OccupancyObservation{
				{ProviderID: "camera", SensorID: "a", State: model.OccupancyOccupied, Sequence: sequence},
				{ProviderID: "camera", SensorID: "b", State: model.OccupancyFree, Sequence: sequence},
			}); err != nil {
				errorsChannel <- err
				return
			}
		}
		errorsChannel <- nil
	}()
	for range 2 {
		if err := <-errorsChannel; err != nil {
			t.Fatal(err)
		}
	}
	if got := fixture.service.BlockState("block-a").State; got != model.OccupancyOccupied {
		t.Fatalf("block-a state=%q", got)
	}
	if got := fixture.service.BlockState("block-b").State; got != model.OccupancyFree {
		t.Fatalf("block-b state=%q", got)
	}
}

func TestOccupancyExpirationConcurrentWithFreshObservation(t *testing.T) {
	fixture := newOccupancyTestFixture(t,
		[]model.OccupancyProvider{occupancyProvider("required", 100, true, time.Minute)},
		[]model.OccupancySensorMapping{occupancyMapping("required", "1", "block")},
	)
	observeOccupancy(t, fixture.service, "required", "1", model.OccupancyFree, 1, nil)
	fixture.clock.Advance(2 * time.Minute)
	start := make(chan struct{})
	errorsChannel := make(chan error, 2)
	go func() {
		<-start
		errorsChannel <- fixture.service.Recompute(context.Background())
	}()
	go func() {
		<-start
		errorsChannel <- fixture.service.Observe(context.Background(), model.OccupancyObservation{
			ProviderID: "required", SensorID: "1", State: model.OccupancyFree,
			Sequence: 2, ObservedAt: fixture.clock.Now(),
		})
	}()
	close(start)
	for range 2 {
		if err := <-errorsChannel; err != nil {
			t.Fatal(err)
		}
	}
	if got := fixture.service.BlockState("block").State; got != model.OccupancyFree {
		t.Fatalf("state=%q", got)
	}
}
