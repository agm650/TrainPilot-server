package topology

import (
	"sort"

	"github.com/agm650/TrainPilot-server/internal/model"
)

// Resource identifies one traversable physical resource.
type Resource struct {
	Kind           EdgeKind
	TrackSectionID string
	TurnoutID      string
}

func (g *Graph) NeighborNodes(nodeID string) []model.TopologyNode {
	if g == nil {
		return nil
	}
	return neighborNodes(g.nodes, nodeID, edgesFromStatic(g, nodeID))
}

func (g *ActiveGraph) NeighborNodes(nodeID string) []model.TopologyNode {
	if g == nil || g.static == nil {
		return nil
	}
	return neighborNodes(g.static.nodes, nodeID, edgesFromActive(g, nodeID))
}

func (g *Graph) TrackSectionsAtNode(nodeID string) []model.TrackSection {
	if g == nil {
		return nil
	}
	return trackSectionsAtNode(g.sections, edgesFromStatic(g, nodeID))
}

func (g *ActiveGraph) TrackSectionsAtNode(nodeID string) []model.TrackSection {
	if g == nil || g.static == nil {
		return nil
	}
	return trackSectionsAtNode(g.static.sections, edgesFromActive(g, nodeID))
}

func (g *Graph) AdjacentTrackSections(sectionID string) []model.TrackSection {
	if g == nil {
		return nil
	}
	section, exists := g.sections[sectionID]
	if !exists {
		return nil
	}
	return adjacentTrackSections(sectionID, g.sections,
		edgesFromStatic(g, section.NodeAID), edgesFromStatic(g, section.NodeBID))
}

func (g *ActiveGraph) AdjacentTrackSections(sectionID string) []model.TrackSection {
	if g == nil || g.static == nil {
		return nil
	}
	section, exists := g.static.sections[sectionID]
	if !exists {
		return nil
	}
	return adjacentTrackSections(sectionID, g.static.sections,
		edgesFromActive(g, section.NodeAID), edgesFromActive(g, section.NodeBID))
}

func (g *Graph) ResourcesAtNode(nodeID string) []Resource {
	if g == nil {
		return nil
	}
	return resourcesFromEdges(edgesFromStatic(g, nodeID))
}

func (g *ActiveGraph) ResourcesAtNode(nodeID string) []Resource {
	if g == nil {
		return nil
	}
	return resourcesFromEdges(edgesFromActive(g, nodeID))
}

func (g *Graph) AdjacentResources(resource Resource) []Resource {
	if g == nil {
		return nil
	}
	resource = canonicalResource(resource)
	nodeIDs, exists := g.resourceNodeIDs(resource)
	if !exists {
		return nil
	}
	return adjacentResources(resource, nodeIDs, g.ResourcesAtNode)
}

func (g *ActiveGraph) AdjacentResources(resource Resource) []Resource {
	if g == nil || g.static == nil {
		return nil
	}
	resource = canonicalResource(resource)
	nodeIDs, exists := g.resourceNodeIDs(resource)
	if !exists {
		return nil
	}
	return adjacentResources(resource, nodeIDs, g.ResourcesAtNode)
}

func neighborNodes(nodes map[string]model.TopologyNode, nodeID string, edges []Edge) []model.TopologyNode {
	if _, exists := nodes[nodeID]; !exists {
		return nil
	}
	neighborIDs := map[string]bool{}
	for _, edge := range edges {
		other, exists := edge.Other(nodeID)
		if exists {
			neighborIDs[other] = true
		}
	}
	ids := make([]string, 0, len(neighborIDs))
	for id := range neighborIDs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	neighbors := make([]model.TopologyNode, 0, len(ids))
	for _, id := range ids {
		neighbors = append(neighbors, nodes[id])
	}
	return neighbors
}

func trackSectionsAtNode(sections map[string]model.TrackSection, edges []Edge) []model.TrackSection {
	ids := map[string]bool{}
	for _, edge := range edges {
		if edge.Kind == EdgeKindTrackSection {
			ids[edge.TrackSectionID] = true
		}
	}
	ordered := make([]string, 0, len(ids))
	for id := range ids {
		ordered = append(ordered, id)
	}
	sort.Strings(ordered)
	result := make([]model.TrackSection, 0, len(ordered))
	for _, id := range ordered {
		result = append(result, sections[id])
	}
	return result
}

func adjacentTrackSections(sectionID string, sections map[string]model.TrackSection, edgeGroups ...[]Edge) []model.TrackSection {
	ids := map[string]bool{}
	for _, edges := range edgeGroups {
		for _, edge := range edges {
			if edge.Kind == EdgeKindTrackSection && edge.TrackSectionID != sectionID {
				ids[edge.TrackSectionID] = true
			}
		}
	}
	ordered := make([]string, 0, len(ids))
	for id := range ids {
		ordered = append(ordered, id)
	}
	sort.Strings(ordered)
	result := make([]model.TrackSection, 0, len(ordered))
	for _, id := range ordered {
		result = append(result, sections[id])
	}
	return result
}

func resourcesFromEdges(edges []Edge) []Resource {
	resources := map[Resource]bool{}
	for _, edge := range edges {
		resource := Resource{Kind: edge.Kind, TrackSectionID: edge.TrackSectionID, TurnoutID: edge.TurnoutID}
		resources[resource] = true
	}
	result := make([]Resource, 0, len(resources))
	for resource := range resources {
		result = append(result, resource)
	}
	sort.Slice(result, func(i, j int) bool { return resourceLess(result[i], result[j]) })
	return result
}

func adjacentResources(resource Resource, nodeIDs []string, resourcesAtNode func(string) []Resource) []Resource {
	resources := map[Resource]bool{}
	for _, nodeID := range nodeIDs {
		for _, adjacent := range resourcesAtNode(nodeID) {
			if adjacent != resource {
				resources[adjacent] = true
			}
		}
	}
	result := make([]Resource, 0, len(resources))
	for adjacent := range resources {
		result = append(result, adjacent)
	}
	sort.Slice(result, func(i, j int) bool { return resourceLess(result[i], result[j]) })
	return result
}

func (g *Graph) resourceNodeIDs(resource Resource) ([]string, bool) {
	switch resource.Kind {
	case EdgeKindTrackSection:
		section, exists := g.sections[resource.TrackSectionID]
		if !exists {
			return nil, false
		}
		return []string{section.NodeAID, section.NodeBID}, true
	case EdgeKindTurnout:
		topology, exists := g.turnoutTopologies[resource.TurnoutID]
		if !exists {
			return nil, false
		}
		nodeIDs := make([]string, 0, len(topology.Ports))
		for _, port := range topology.Ports {
			nodeIDs = append(nodeIDs, port.NodeID)
		}
		sort.Strings(nodeIDs)
		return nodeIDs, true
	default:
		return nil, false
	}
}

func (g *ActiveGraph) resourceNodeIDs(resource Resource) ([]string, bool) {
	if resource.Kind == EdgeKindTrackSection {
		return g.static.resourceNodeIDs(resource)
	}
	if resource.Kind != EdgeKindTurnout {
		return nil, false
	}
	nodes := map[string]bool{}
	for _, edge := range g.edges {
		if edge.TurnoutID == resource.TurnoutID {
			nodes[edge.NodeAID] = true
			nodes[edge.NodeBID] = true
		}
	}
	if len(nodes) == 0 {
		return nil, false
	}
	nodeIDs := make([]string, 0, len(nodes))
	for nodeID := range nodes {
		nodeIDs = append(nodeIDs, nodeID)
	}
	sort.Strings(nodeIDs)
	return nodeIDs, true
}

func edgesFromStatic(g *Graph, nodeID string) []Edge {
	indexes, exists := g.incident[nodeID]
	if !exists {
		return nil
	}
	edges := make([]Edge, 0, len(indexes))
	for _, index := range indexes {
		edges = append(edges, g.edges[index])
	}
	return edges
}

func edgesFromActive(g *ActiveGraph, nodeID string) []Edge {
	indexes, exists := g.incident[nodeID]
	if !exists {
		return nil
	}
	edges := make([]Edge, 0, len(indexes))
	for _, index := range indexes {
		edges = append(edges, g.edges[index].Edge)
	}
	return edges
}

func resourceLess(a, b Resource) bool {
	if a.Kind != b.Kind {
		return a.Kind == EdgeKindTrackSection
	}
	if a.Kind == EdgeKindTrackSection {
		return a.TrackSectionID < b.TrackSectionID
	}
	return a.TurnoutID < b.TurnoutID
}

func canonicalResource(resource Resource) Resource {
	if resource.Kind == EdgeKindTrackSection {
		resource.TurnoutID = ""
	}
	if resource.Kind == EdgeKindTurnout {
		resource.TrackSectionID = ""
	}
	return resource
}
