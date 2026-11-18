package service

import (
	"reflect"
	"testing"

	"github.com/agm650/TrainPilot-server/internal/model"
)

func balInput(id string) BALSignalInput {
	p := model.SNCFBAL3Profile()
	d := model.SignalDefinition{ID: id, Name: id, ProfileID: p.ID, Mode: model.SignalModeAutomatic, Placement: model.SignalPlacement{TrackSectionID: "section-" + id, EntryNodeID: "entry-" + id}, ProtectedBlockID: "block-" + id, Downstream: model.SignalDownstream{Kind: model.SignalDownstreamTerminal, Aspect: "S"}, Endpoint: model.SignalEndpoint{Kind: model.SignalEndpointDCCExtended, LinearAddress: 1, AspectCodes: map[model.SignalAspectID]int{"S": 0, "A": 7, "VL": 16}}}
	d, _ = model.NormalizeSignalDefinition(d, p)
	value := model.SignalAspectID("A")
	return BALSignalInput{Definition: d, Profile: p, Occupancy: model.OccupancyFree, Available: true, State: model.SignalState{ID: id, DesiredAspect: value, CommandedAspect: &value, Synchronized: true}}
}

func TestBALMatrixAndExplicitTerminal(t *testing.T) {
	for _, occupied := range []model.OccupancyState{model.OccupancyUnknown, model.OccupancyOccupied, model.OccupancyFree, "invalid"} {
		for _, available := range []bool{false, true} {
			for _, downAspect := range []model.SignalAspectID{"S", "A", "VL", "unknown"} {
				a, b := balInput("a"), balInput("b")
				a.Definition.Downstream = model.SignalDownstream{Kind: model.SignalDownstreamSignal, SignalID: "b"}
				a.Occupancy = occupied
				a.Available = available
				value := downAspect
				b.State.DesiredAspect = value
				b.State.CommandedAspect = &value
				want := model.SignalAspectID("S")
				if available && occupied == model.OccupancyFree {
					want = "A"
					if value == "A" || value == "VL" {
						want = "VL"
					}
				}
				got := CalculateBAL(map[string]BALSignalInput{"a": a, "b": b})["a"]
				if got.Aspect != want {
					t.Fatalf("occupancy=%s available=%v downstream=%s: %+v want %s", occupied, available, value, got, want)
				}
			}
		}
	}
	got := CalculateBAL(map[string]BALSignalInput{"a": balInput("a")})["a"]
	if got.Aspect != "A" || got.Reason != "terminal_stop" {
		t.Fatal(got)
	}
}

func TestBALRejectsInvalidConfigurationAndUnusableDownstream(t *testing.T) {
	for _, change := range []func(*BALSignalInput){
		func(i *BALSignalInput) { i.Definition.Downstream = model.SignalDownstream{} },
		func(i *BALSignalInput) {
			i.Definition.Downstream = model.SignalDownstream{Kind: model.SignalDownstreamSignal, SignalID: "missing"}
		},
		func(i *BALSignalInput) { i.Definition.Downstream.Aspect = "VL" },
		func(i *BALSignalInput) { i.Definition.Endpoint.LinearAddress = 0 },
		func(i *BALSignalInput) { i.Profile.Aspects[0].Role = model.SignalRoleProceed },
	} {
		a := balInput("a")
		change(&a)
		if got := CalculateBAL(map[string]BALSignalInput{"a": a})["a"]; got.Aspect != "S" || got.Synchronized {
			t.Fatal(got)
		}
	}
	for _, change := range []func(*BALSignalInput){
		func(i *BALSignalInput) { i.State.Pending = true; i.State.DesiredAspect = "S" }, func(i *BALSignalInput) { i.State.Synchronized = false },
		func(i *BALSignalInput) { i.State.CommandedAspect = nil }, func(i *BALSignalInput) { i.State.Error = &model.SignalError{Code: "failed"} },
		func(i *BALSignalInput) { v := model.SignalAspectID("S"); i.State.ReportedAspect = &v },
		func(i *BALSignalInput) { i.Available = false }, func(i *BALSignalInput) { i.Occupancy = model.OccupancyUnknown },
	} {
		a, b := balInput("a"), balInput("b")
		a.Definition.Downstream = model.SignalDownstream{Kind: model.SignalDownstreamSignal, SignalID: "b"}
		change(&b)
		if got := CalculateBAL(map[string]BALSignalInput{"a": a, "b": b})["a"]; got.Aspect != "A" {
			t.Fatal(got)
		}
	}
}

func TestBALCyclesAreDeterministicAndDoNotMutateInputs(t *testing.T) {
	a, b := balInput("a"), balInput("b")
	a.Definition.Downstream = model.SignalDownstream{Kind: model.SignalDownstreamSignal, SignalID: "b"}
	b.Definition.Downstream = model.SignalDownstream{Kind: model.SignalDownstreamSignal, SignalID: "a"}
	inputs := map[string]BALSignalInput{"a": a, "b": b}
	for range 100 {
		got := CalculateBAL(inputs)
		if got["a"].Aspect != "VL" || got["b"].Aspect != "VL" {
			t.Fatal(got)
		}
	}
	if !reflect.DeepEqual(inputs, map[string]BALSignalInput{"b": b, "a": a}) {
		t.Fatal("inputs mutated")
	}
	b.Occupancy = model.OccupancyOccupied
	inputs["b"] = b
	if got := CalculateBAL(inputs); got["b"].Aspect != "S" || got["a"].Aspect != "A" {
		t.Fatal(got)
	}
}

func TestBALUsesProfileRolesAndAcceptsReliableManualDownstream(t *testing.T) {
	a, b := balInput("a"), balInput("b")
	p := a.Profile
	p.ID = "custom"
	p.FallbackAspect = "HALT"
	names := map[model.SignalAspectID]model.SignalAspectID{"S": "HALT", "A": "WARN", "VL": "GO"}
	for index := range p.Aspects {
		p.Aspects[index].ID = names[p.Aspects[index].ID]
	}
	a.Profile = p
	a.Definition.ProfileID = p.ID
	a.Definition.FallbackAspect = "HALT"
	a.Definition.Endpoint.AspectCodes = map[model.SignalAspectID]int{"HALT": 23, "WARN": 4, "GO": 99}
	a.Definition.Downstream = model.SignalDownstream{Kind: model.SignalDownstreamSignal, SignalID: "b"}
	b.Definition.Mode = model.SignalModeManual
	if got := CalculateBAL(map[string]BALSignalInput{"a": a, "b": b})["a"]; got.Aspect != "GO" {
		t.Fatal(got)
	}
	b.Occupancy = model.OccupancyUnknown
	if got := CalculateBAL(map[string]BALSignalInput{"a": a, "b": b})["a"]; got.Aspect != "WARN" {
		t.Fatal(got)
	}
}
