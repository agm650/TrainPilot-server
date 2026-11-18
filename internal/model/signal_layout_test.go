package model

import (
	"errors"
	"reflect"
	"testing"
)

func signalLineLayout() LayoutDefinition {
	p := customSignalProfile()
	a := signalForProfile(p)
	a.ID = "a"
	a.Placement = SignalPlacement{TrackSectionID: "ab", EntryNodeID: "a"}
	a.ProtectedBlockID = "block-a"
	a.Downstream = SignalDownstream{Kind: SignalDownstreamSignal, SignalID: "b"}
	b := signalForProfile(p)
	b.ID = "b"
	b.Placement = SignalPlacement{TrackSectionID: "bc", EntryNodeID: "b"}
	b.ProtectedBlockID = "block-b"
	b.Endpoint.LinearAddress = 2
	return LayoutDefinition{SignalProfiles: []SignalProfile{p}, Signals: []SignalDefinition{a, b}, TopologyNodes: []TopologyNode{{ID: "a", Kind: TopologyNodeBoundary}, {ID: "b", Kind: TopologyNodeJoint}, {ID: "c", Kind: TopologyNodeBoundary}}, TrackSections: []TrackSection{{ID: "ab", Name: "AB", NodeAID: "a", NodeBID: "b"}, {ID: "bc", Name: "BC", NodeAID: "b", NodeBID: "c"}}, Blocks: []BlockDefinition{{ID: "block-a", Name: "A", TrackSectionIDs: []string{"ab"}}, {ID: "block-b", Name: "B", TrackSectionIDs: []string{"bc"}}}}
}

func TestSignalLayoutValidationAndCoverage(t *testing.T) {
	for name, change := range map[string]func(*LayoutDefinition){
		"linear": func(*LayoutDefinition) {},
		"opposed boundary signals": func(l *LayoutDefinition) {
			l.Signals[0].Placement.EntryNodeID = "b"
			l.Signals[0].Downstream = SignalDownstream{Kind: SignalDownstreamTerminal, Aspect: "HALT"}
		},
		"cyclic downstream": func(l *LayoutDefinition) {
			l.TopologyNodes = l.TopologyNodes[:2]
			for i := range l.TopologyNodes {
				l.TopologyNodes[i].Kind = TopologyNodeJoint
			}
			l.TrackSections[1].NodeBID = "a"
			l.Signals[1].Downstream = SignalDownstream{Kind: SignalDownstreamSignal, SignalID: "a"}
		},
		"self loop": func(l *LayoutDefinition) {
			l.TopologyNodes = l.TopologyNodes[:2]
			for i := range l.TopologyNodes {
				l.TopologyNodes[i].Kind = TopologyNodeJoint
			}
			l.TrackSections[1].NodeBID = "a"
			l.Blocks = l.Blocks[:1]
			l.Blocks[0].TrackSectionIDs = []string{"ab", "bc"}
			l.Signals = l.Signals[:1]
			l.Signals[0].Downstream.SignalID = "a"
		},
	} {
		t.Run(name, func(t *testing.T) {
			l := signalLineLayout()
			change(&l)
			if err := ValidateSignalDefinitions(l); err != nil {
				t.Fatal(err)
			}
		})
	}
	l := signalLineLayout()
	l.Signals = l.Signals[1:]
	if got := UnsignalledBlockIDs(l); !reflect.DeepEqual(got, []string{"block-a"}) {
		t.Fatal(got)
	}
	if err := ValidateSignalDefinitions(LayoutDefinition{}); err != nil {
		t.Fatal("empty legacy layout rejected", err)
	}
}

func TestSignalLayoutRejectsInconsistentReferences(t *testing.T) {
	cases := map[string]func(*LayoutDefinition){
		"unknown profile":    func(l *LayoutDefinition) { l.SignalProfiles = nil },
		"orphan section":     func(l *LayoutDefinition) { l.Signals[0].Placement.TrackSectionID = "missing" },
		"orphan node":        func(l *LayoutDefinition) { l.Signals[0].Placement.EntryNodeID = "missing" },
		"wrong owner":        func(l *LayoutDefinition) { l.Signals[0].ProtectedBlockID = "block-b" },
		"orphan downstream":  func(l *LayoutDefinition) { l.Signals[0].Downstream.SignalID = "missing" },
		"wrong orientation":  func(l *LayoutDefinition) { l.Signals[0].Placement.EntryNodeID = "b" },
		"duplicate endpoint": func(l *LayoutDefinition) { l.Signals[1].Endpoint.LinearAddress = 1 },
		"duplicate placement": func(l *LayoutDefinition) {
			l.Signals[1].Placement = l.Signals[0].Placement
			l.Signals[1].ProtectedBlockID = "block-a"
		},
		"duplicate id":      func(l *LayoutDefinition) { l.Signals[1].ID = "a" },
		"duplicate profile": func(l *LayoutDefinition) { l.SignalProfiles = append(l.SignalProfiles, l.SignalProfiles[0]) },
		"uncovered gap":     func(l *LayoutDefinition) { l.Signals[1].Placement.EntryNodeID = "c" },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			l := signalLineLayout()
			change(&l)
			if err := ValidateSignalDefinitions(l); !errors.Is(err, ErrInvalidSignalLayout) {
				t.Fatal("invalid layout accepted", err)
			}
		})
	}
}
