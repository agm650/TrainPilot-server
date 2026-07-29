package service

import (
	"context"
	"errors"

	"github.com/agm650/TrainPilot-server/internal/events"
	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/station"
	"github.com/agm650/TrainPilot-server/internal/store"
)

type RailwayService struct {
	store   *store.Store
	station station.CommandStation
	events  *events.Bus
}

func NewRailwayService(s *store.Store, st station.CommandStation, b *events.Bus) *RailwayService {
	return &RailwayService{store: s, station: st, events: b}
}
func (r *RailwayService) Locomotives(ctx context.Context) ([]model.Locomotive, error) {
	return r.store.ListLocomotives(ctx)
}
func (r *RailwayService) Blocks(ctx context.Context) ([]model.Block, error) {
	return r.store.ListBlocks(ctx)
}
func (r *RailwayService) Turnouts(ctx context.Context) ([]model.Turnout, error) {
	return r.store.ListTurnouts(ctx)
}
func (r *RailwayService) SetTurnout(ctx context.Context, user model.User, id, state string) error {
	if !Allowed(user.Role, PermissionDispatch) {
		return errors.New("permission denied")
	}
	if state != "straight" && state != "diverging" {
		return errors.New("state must be straight or diverging")
	}
	t, err := r.store.GetTurnout(ctx, id)
	if err != nil {
		return err
	}
	if err := r.station.SetAccessory(ctx, t.DCCAddress, state); err != nil {
		return err
	}
	if err := r.store.SetTurnoutState(ctx, id, state); err != nil {
		return err
	}
	r.events.Publish("turnout.state.changed", map[string]any{"turnoutId": id, "state": state})
	return nil
}
func (r *RailwayService) SetBlockFeedback(ctx context.Context, id string, occupied bool) error {
	if err := r.store.SetBlockOccupied(ctx, id, occupied); err != nil {
		return err
	}
	r.events.Publish("block.occupancy.changed", map[string]any{"blockId": id, "occupied": occupied})
	return nil
}

func (r *RailwayService) StartFeedback(ctx context.Context) {
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-r.station.Feedback():
				if !ok {
					return
				}
				blockID, err := r.store.BlockForFeedback(ctx, event.Source, event.Address)
				if err != nil {
					continue
				}
				_ = r.SetBlockFeedback(ctx, blockID, event.Active)
			}
		}
	}()
}
