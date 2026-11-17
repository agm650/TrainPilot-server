package service

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"time"

	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/station"
	"github.com/agm650/TrainPilot-server/internal/store"
)

type feedbackRecord struct {
	event           station.FeedbackEvent
	result, blockID string
	err             error
}

func (r *RailwayService) handleFeedback(ctx context.Context, event station.FeedbackEvent) {
	started := time.Now()
	provider, synchronized := r.station.(station.FeedbackStateProvider)
	if synchronized {
		if err := r.occupancy.SyncStationFeedback(ctx, provider); err != nil {
			return
		}
		state := provider.FeedbackState()
		if event.Kind == "synchronization" || !state.Available || event.Generation != state.Generation || event.PowerGeneration != state.PowerGeneration {
			return
		}
	}
	if event.Source == "z21-rbus" && (event.Generation != r.feedbackLogGeneration || event.PowerGeneration != r.feedbackLogPowerGeneration) {
		r.feedbackLogGeneration = event.Generation
		r.feedbackLogPowerGeneration = event.PowerGeneration
		r.rbusLogActive = nil
	}
	inputs := event.Inputs
	if inputs == nil {
		inputs = []station.FeedbackInput{{Address: event.Address, Active: event.Active}}
	}
	observations := make([]model.OccupancyObservation, 0, len(inputs))
	records := make([]feedbackRecord, 0, len(inputs))
	before := make(map[string]model.OccupancyState)
	var mappingError error
	for _, input := range inputs {
		individual := station.FeedbackEvent{Source: event.Source, Kind: event.Kind, Address: input.Address, Active: input.Active, Generation: event.Generation}
		sensorID, providerID := strconv.Itoa(input.Address), event.Source
		mapping, err := r.store.ResolveOccupancySensorMapping(ctx, providerID, sensorID)
		if errors.Is(err, store.ErrNotFound) {
			providerID = "*"
			mapping, err = r.store.ResolveOccupancySensorMapping(ctx, providerID, sensorID)
		}
		record := feedbackRecord{event: individual, result: "mapped", blockID: mapping.BlockID, err: err}
		if err != nil {
			record.result = "unmapped"
			if !errors.Is(err, store.ErrNotFound) {
				record.result, mappingError = "mapping_error", err
			}
		} else {
			state := model.OccupancyFree
			if input.Active {
				state = model.OccupancyOccupied
			}
			before[mapping.BlockID] = r.occupancy.BlockState(mapping.BlockID).State
			observations = append(observations, model.OccupancyObservation{
				ProviderID: providerID, SensorID: sensorID, State: state,
				Sequence: r.nextFeedbackSequence(providerID, sensorID), ObservedAt: time.Now().UTC(),
			})
		}
		records = append(records, record)
	}
	err := mappingError
	if err == nil && len(observations) > 0 {
		if synchronized {
			err = r.occupancy.ObserveStationBatch(ctx, observations, provider, event.Generation, event.PowerGeneration)
		} else {
			err = r.occupancy.ObserveBatch(ctx, observations)
		}
	}
	counted := make(map[string]bool)
	for i := range records {
		record := &records[i]
		if record.result == "mapped" {
			if err != nil {
				record.result, record.err = "update_error", err
			} else {
				after := r.occupancy.BlockState(record.blockID).State
				changed := before[record.blockID] != after && !counted[record.blockID]
				r.metrics.ObserveFeedbackOccupancy(event.Source, changed)
				counted[record.blockID] = true
			}
		}
		r.metrics.ObserveFeedback(event.Source, record.result, time.Since(started))
	}
	r.logFeedbackRecords(records)
}

func (r *RailwayService) logFeedbackRecords(records []feedbackRecord) {
	if r.feedbackLogger == nil {
		return
	}
	changed := make([]bool, len(records))
	var occupied []int
	for i, record := range records {
		if record.event.Source != "z21-rbus" || record.event.Kind != "occupancy" {
			changed[i] = true
			continue
		}
		if r.rbusLogActive == nil {
			r.rbusLogActive = make(map[int]bool)
		}
		changed[i] = r.rbusLogActive[record.event.Address] != record.event.Active
		r.rbusLogActive[record.event.Address] = record.event.Active
	}
	if r.rbusLogActive != nil {
		occupied = make([]int, 0)
		for address, active := range r.rbusLogActive {
			if active {
				occupied = append(occupied, address)
			}
		}
		sort.Ints(occupied)
	}
	for i, record := range records {
		if changed[i] {
			addresses := occupied
			if record.event.Source != "z21-rbus" || record.event.Kind != "occupancy" {
				addresses = nil
			}
			r.emitFeedbackLog(record.event, record.result, record.blockID, record.err, addresses)
		}
	}
}
