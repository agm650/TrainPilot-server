package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/station"
)

type feedbackStateStub struct {
	mu    sync.Mutex
	state station.FeedbackState
}

func (p *feedbackStateStub) FeedbackState() station.FeedbackState {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state
}

func (p *feedbackStateStub) set(state station.FeedbackState) {
	p.mu.Lock()
	p.state = state
	p.mu.Unlock()
}

func TestStationFeedbackFencesGenerationsIncludingWildcard(t *testing.T) {
	for _, mappingProvider := range []string{"z21-rbus", "*"} {
		t.Run(mappingProvider, func(t *testing.T) {
			ctx := context.Background()
			f := newOccupancyTestFixture(t,
				[]model.OccupancyProvider{occupancyProvider(mappingProvider, 100, true, 0)},
				[]model.OccupancySensorMapping{occupancyMapping(mappingProvider, "1", "block-a"), occupancyMapping(mappingProvider, "81", "block-b")})
			provider := &feedbackStateStub{state: station.FeedbackState{Source: "z21-rbus", Generation: 1, Available: true}}
			observation := model.OccupancyObservation{ProviderID: mappingProvider, SensorID: "1", State: model.OccupancyFree, Sequence: 1, ObservedAt: time.Now().UTC()}
			if err := f.service.ObserveStationBatch(ctx, []model.OccupancyObservation{observation}, provider, 1); err != nil {
				t.Fatal(err)
			}
			provider.set(station.FeedbackState{Source: "z21-rbus", Generation: 2, Available: false})
			if err := f.service.SyncStationFeedback(ctx, provider); err != nil {
				t.Fatal(err)
			}
			if got := f.service.BlockState("block-a").State; got != model.OccupancyUnknown {
				t.Fatalf("loss state=%s", got)
			}
			provider.set(station.FeedbackState{Source: "z21-rbus", Generation: 2, Available: true})
			observation.Sequence++
			if err := f.service.ObserveStationBatch(ctx, []model.OccupancyObservation{observation}, provider, 1); !errors.Is(err, ErrFeedbackGenerationStale) {
				t.Fatalf("old generation error=%v", err)
			}
			if got := f.service.BlockState("block-a").State; got != model.OccupancyUnknown {
				t.Fatalf("online resurrected an old observation: %s", got)
			}
			observation.State = model.OccupancyOccupied
			if err := f.service.ObserveStationBatch(ctx, []model.OccupancyObservation{observation}, provider, 2); err != nil {
				t.Fatal(err)
			}
			if f.service.BlockState("block-a").State != model.OccupancyOccupied || f.service.BlockState("block-b").State != model.OccupancyUnknown {
				t.Fatal("partial synchronization restored an unobserved block")
			}
		})
	}
}
