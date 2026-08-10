package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

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
func (r *RailwayService) Locomotive(ctx context.Context, id string) (model.Locomotive, error) {
	return r.store.GetLocomotive(ctx, id)
}

func (r *RailwayService) CreateLocomotive(ctx context.Context, user model.User, input model.LocomotiveInput) (model.Locomotive, error) {
	if !Allowed(user.Role, PermissionConfigure) {
		return model.Locomotive{}, errors.New("permission denied")
	}
	x, err := locomotiveFromInput(newID(), input)
	if err != nil {
		return model.Locomotive{}, err
	}
	if err := r.store.CreateLocomotive(ctx, x); err != nil {
		return model.Locomotive{}, err
	}
	r.events.Publish("locomotive.created", x)
	return x, nil
}

func (r *RailwayService) UpdateLocomotive(ctx context.Context, user model.User, id string, input model.LocomotiveInput) (model.Locomotive, error) {
	if !Allowed(user.Role, PermissionConfigure) {
		return model.Locomotive{}, errors.New("permission denied")
	}
	if _, err := r.store.GetLocomotive(ctx, id); err != nil {
		return model.Locomotive{}, err
	}
	if _, err := r.store.LiveLeaseForLoco(ctx, id); err == nil {
		return model.Locomotive{}, fmt.Errorf("locomotive has an active control lease: %w", store.ErrConflict)
	} else if !errors.Is(err, store.ErrNotFound) {
		return model.Locomotive{}, err
	}
	x, err := locomotiveFromInput(id, input)
	if err != nil {
		return model.Locomotive{}, err
	}
	if err := r.store.UpdateLocomotive(ctx, x); err != nil {
		return model.Locomotive{}, err
	}
	r.events.Publish("locomotive.updated", x)
	return x, nil
}

func (r *RailwayService) DeleteLocomotive(ctx context.Context, user model.User, id string) error {
	if !Allowed(user.Role, PermissionConfigure) {
		return errors.New("permission denied")
	}
	if _, err := r.store.GetLocomotive(ctx, id); err != nil {
		return err
	}
	if _, err := r.store.LiveLeaseForLoco(ctx, id); err == nil {
		return fmt.Errorf("locomotive has an active control lease: %w", store.ErrConflict)
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	if err := r.store.DeleteLocomotive(ctx, id); err != nil {
		return err
	}
	r.events.Publish("locomotive.deleted", map[string]any{"locomotiveId": id})
	return nil
}

func locomotiveFromInput(id string, input model.LocomotiveInput) (model.Locomotive, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.AddressKind = strings.ToLower(strings.TrimSpace(input.AddressKind))
	input.Manufacturer = strings.TrimSpace(input.Manufacturer)
	input.Model = strings.TrimSpace(input.Model)

	if input.Name == "" {
		return model.Locomotive{}, errors.New("name is required")
	}
	if len(input.Name) > 128 {
		return model.Locomotive{}, errors.New("name must be at most 128 characters")
	}
	if len(input.Manufacturer) > 128 {
		return model.Locomotive{}, errors.New("manufacturer must be at most 128 characters")
	}
	if len(input.Model) > 128 {
		return model.Locomotive{}, errors.New("model must be at most 128 characters")
	}
	if input.DCCAddress < 1 || input.DCCAddress > 10239 {
		return model.Locomotive{}, errors.New("dccAddress must be in range 1..10239")
	}
	switch input.AddressKind {
	case "short":
		if input.DCCAddress > 127 {
			return model.Locomotive{}, errors.New("short dccAddress must be in range 1..127")
		}
	case "long":
		if input.DCCAddress < 128 {
			return model.Locomotive{}, errors.New("long dccAddress must be in range 128..10239")
		}
	default:
		return model.Locomotive{}, errors.New("addressKind must be short or long")
	}
	switch input.SpeedSteps {
	case 14, 28, 128:
	default:
		return model.Locomotive{}, errors.New("speedSteps must be one of 14, 28 or 128")
	}

	return model.Locomotive{
		ID:           id,
		Name:         input.Name,
		DCCAddress:   input.DCCAddress,
		AddressKind:  input.AddressKind,
		SpeedSteps:   input.SpeedSteps,
		Manufacturer: input.Manufacturer,
		Model:        input.Model,
	}, nil
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
