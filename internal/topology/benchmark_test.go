package topology_test

import (
	"fmt"
	"testing"

	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/topology"
)

var benchmarkPath topology.Path

func BenchmarkTopologyBuild(b *testing.B) {
	for _, sectionCount := range []int{100, 1000} {
		b.Run(fmt.Sprintf("sections-%d", sectionCount), func(b *testing.B) {
			layout := benchmarkLinearLayout(sectionCount)
			b.ReportAllocs()
			b.ResetTimer()
			for iteration := 0; iteration < b.N; iteration++ {
				if _, err := topology.Build(layout); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkFindPath(b *testing.B) {
	for _, sectionCount := range []int{100, 1000} {
		b.Run(fmt.Sprintf("sections-%d", sectionCount), func(b *testing.B) {
			graph, err := topology.Build(benchmarkLinearLayout(sectionCount))
			if err != nil {
				b.Fatal(err)
			}
			fromNodeID := benchmarkNodeID(0)
			toNodeID := benchmarkNodeID(sectionCount)
			b.ReportAllocs()
			b.ResetTimer()
			for iteration := 0; iteration < b.N; iteration++ {
				path, found := graph.FindPath(fromNodeID, toNodeID, topology.PathConstraints{})
				if !found {
					b.Fatal("path not found")
				}
				benchmarkPath = path
			}
		})
	}
}

func benchmarkLinearLayout(sectionCount int) model.LayoutDefinition {
	layout := model.LayoutDefinition{
		TopologyNodes: make([]model.TopologyNode, 0, sectionCount+1),
		TrackSections: make([]model.TrackSection, 0, sectionCount),
	}
	for index := 0; index <= sectionCount; index++ {
		kind := model.TopologyNodeJoint
		if index == 0 || index == sectionCount {
			kind = model.TopologyNodeBoundary
		}
		layout.TopologyNodes = append(layout.TopologyNodes, model.TopologyNode{
			ID: benchmarkNodeID(index), Kind: kind,
		})
	}
	for index := 0; index < sectionCount; index++ {
		layout.TrackSections = append(layout.TrackSections, model.TrackSection{
			ID:      fmt.Sprintf("section-%04d", index),
			Name:    fmt.Sprintf("Section %d", index),
			NodeAID: benchmarkNodeID(index),
			NodeBID: benchmarkNodeID(index + 1),
		})
	}
	return layout
}

func benchmarkNodeID(index int) string {
	return fmt.Sprintf("node-%04d", index)
}
