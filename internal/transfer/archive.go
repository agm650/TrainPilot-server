package transfer

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/agm650/TrainPilot-server/internal/clock"
	"github.com/agm650/TrainPilot-server/internal/events"
	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/service"
	"github.com/agm650/TrainPilot-server/internal/store"
	"github.com/agm650/TrainPilot-server/internal/topology"
)

const (
	FormatID            = "org.dcc-control.package"
	FormatVersion       = 6 // rolling-stock archives
	LayoutFormatVersion = 7
	OldestVersion       = 1
	MaxArchiveSize      = 25 << 20
	MaxEntrySize        = 10 << 20
)

var ErrInvalidArchive = errors.New("invalid archive")

type Manifest struct {
	Format      string    `json:"format"`
	Version     int       `json:"version"`
	PackageType string    `json:"packageType"`
	CreatedAt   time.Time `json:"createdAt"`
}

type RollingStockDocument struct {
	Locomotives []model.Locomotive `json:"locomotives"`
}
type LayoutDocument struct {
	Layout model.LayoutDefinition `json:"layout"`
}

type layoutTurnoutDefinition struct {
	ID        string                            `json:"id"`
	Name      string                            `json:"name"`
	Kind      model.TurnoutKind                 `json:"kind"`
	Endpoints []model.AccessoryEndpoint         `json:"endpoints"`
	Positions []model.TurnoutPositionDefinition `json:"positions"`
}

type layoutArchiveBlockDefinition struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	TrackSectionIDs []string `json:"trackSectionIds"`
	TurnoutIDs      []string `json:"turnoutIds,omitempty"`
	Occupied        *bool    `json:"occupied,omitempty"`
}

type layoutArchiveDefinition struct {
	Presentation            model.LayoutPresentation       `json:"presentation"`
	Nodes                   []model.TopologyNode           `json:"nodes"`
	TrackSections           []model.TrackSection           `json:"trackSections"`
	TurnoutTopologies       []model.TurnoutTopology        `json:"turnoutTopologies"`
	Blocks                  []layoutArchiveBlockDefinition `json:"blocks"`
	Turnouts                []layoutTurnoutDefinition      `json:"turnouts"`
	Routes                  []model.RouteDefinition        `json:"routes"`
	FeedbackMappings        []model.FeedbackMapping        `json:"feedbackMappings"`
	OccupancyProviders      []occupancyArchiveProvider     `json:"occupancyProviders,omitempty"`
	OccupancySensorMappings []model.OccupancySensorMapping `json:"occupancySensorMappings,omitempty"`
}

type occupancyArchiveProvider struct {
	ID                string `json:"id"`
	Type              string `json:"type"`
	Priority          int    `json:"priority"`
	Required          bool   `json:"required"`
	StaleAfter        string `json:"staleAfter"`
	FreshnessRequired bool   `json:"freshnessRequired"`
}

// MarshalJSON deliberately exports turnout configuration separately from its
// operational state. A layout archive must not restore a pending command or a
// last observed position when imported on another server.
func (d LayoutDocument) MarshalJSON() ([]byte, error) {
	presentation := model.EmptyLayoutPresentation()
	if d.Layout.Presentation != nil {
		presentation = model.NormalizeLayoutPresentation(*d.Layout.Presentation)
	}
	blocks := make([]layoutArchiveBlockDefinition, 0, len(d.Layout.Blocks))
	for _, block := range d.Layout.Blocks {
		blocks = append(blocks, layoutArchiveBlockDefinition{
			ID: block.ID, Name: block.Name,
			TrackSectionIDs: append([]string{}, block.TrackSectionIDs...), TurnoutIDs: block.TurnoutIDs,
		})
	}
	turnouts := make([]layoutTurnoutDefinition, 0, len(d.Layout.Turnouts))
	for _, turnout := range d.Layout.Turnouts {
		turnouts = append(turnouts, layoutTurnoutDefinition{
			ID: turnout.ID, Name: turnout.Name, Kind: turnout.Kind,
			Endpoints: turnout.Endpoints, Positions: turnout.Positions,
		})
	}
	providers := make([]occupancyArchiveProvider, 0, len(d.Layout.OccupancyProviders))
	for _, provider := range d.Layout.OccupancyProviders {
		providers = append(providers, occupancyArchiveProvider{
			ID: provider.ID, Type: provider.Type, Priority: provider.Priority,
			Required: provider.Required, StaleAfter: provider.StaleAfter.String(),
			FreshnessRequired: provider.FreshnessRequired,
		})
	}
	return json.Marshal(struct {
		Layout layoutArchiveDefinition `json:"layout"`
	}{Layout: layoutArchiveDefinition{
		Presentation: presentation,
		Nodes:        d.Layout.TopologyNodes, TrackSections: d.Layout.TrackSections,
		TurnoutTopologies: d.Layout.TurnoutTopologies,
		Blocks:            blocks, Turnouts: turnouts, Routes: d.Layout.Routes,
		FeedbackMappings:        d.Layout.FeedbackMappings,
		OccupancyProviders:      providers,
		OccupancySensorMappings: d.Layout.OccupancySensorMappings,
	}})
}

func (d *LayoutDocument) UnmarshalJSON(data []byte) error {
	var document struct {
		Layout struct {
			Presentation            *model.LayoutPresentation      `json:"presentation"`
			Nodes                   []model.TopologyNode           `json:"nodes"`
			TrackSections           []model.TrackSection           `json:"trackSections"`
			TurnoutTopologies       []model.TurnoutTopology        `json:"turnoutTopologies"`
			Blocks                  []layoutArchiveBlockDefinition `json:"blocks"`
			Turnouts                []model.Turnout                `json:"turnouts"`
			Routes                  []model.RouteDefinition        `json:"routes"`
			FeedbackMappings        []model.FeedbackMapping        `json:"feedbackMappings"`
			OccupancyProviders      []occupancyArchiveProvider     `json:"occupancyProviders,omitempty"`
			OccupancySensorMappings []model.OccupancySensorMapping `json:"occupancySensorMappings,omitempty"`
		} `json:"layout"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return err
	}
	blocks := make([]model.BlockDefinition, 0, len(document.Layout.Blocks))
	for _, block := range document.Layout.Blocks {
		blocks = append(blocks, model.BlockDefinition{
			ID: block.ID, Name: block.Name,
			TrackSectionIDs: block.TrackSectionIDs, TurnoutIDs: block.TurnoutIDs,
		})
	}
	providers := make([]model.OccupancyProvider, 0, len(document.Layout.OccupancyProviders))
	for _, provider := range document.Layout.OccupancyProviders {
		staleAfter, err := time.ParseDuration(provider.StaleAfter)
		if err != nil {
			return fmt.Errorf("occupancy provider %q staleAfter: %w", provider.ID, err)
		}
		providers = append(providers, model.OccupancyProvider{
			ID: provider.ID, Type: provider.Type, Priority: provider.Priority,
			Required: provider.Required, StaleAfter: staleAfter,
			FreshnessRequired: provider.FreshnessRequired,
		})
	}
	d.Layout = model.LayoutDefinition{
		Presentation:            document.Layout.Presentation,
		TopologyNodes:           document.Layout.Nodes,
		TrackSections:           document.Layout.TrackSections,
		TurnoutTopologies:       document.Layout.TurnoutTopologies,
		Blocks:                  blocks,
		Turnouts:                document.Layout.Turnouts,
		Routes:                  document.Layout.Routes,
		FeedbackMappings:        document.Layout.FeedbackMappings,
		OccupancyProviders:      providers,
		OccupancySensorMappings: document.Layout.OccupancySensorMappings,
	}
	return nil
}

type Service struct {
	store  *store.Store
	events *events.Bus
	clock  clock.Clock
}

func New(s *store.Store, b *events.Bus, c clock.Clock) *Service {
	return &Service{store: s, events: b, clock: c}
}

func (s *Service) ExportRollingStock(ctx context.Context) ([]byte, error) {
	items, err := s.store.ListLocomotives(ctx)
	if err != nil {
		return nil, err
	}
	return writeArchive(Manifest{Format: FormatID, Version: FormatVersion, PackageType: "rolling-stock", CreatedAt: s.clock.Now()}, "rolling-stock.json", RollingStockDocument{Locomotives: items})
}

// BuildRollingStockArchive creates an importable archive without requiring a
// store. It is intended for deterministic offline data-set generators.
func BuildRollingStockArchive(createdAt time.Time, items []model.Locomotive) ([]byte, error) {
	if err := validateLocomotives(items); err != nil {
		return nil, err
	}
	return writeArchive(Manifest{
		Format: FormatID, Version: FormatVersion, PackageType: "rolling-stock", CreatedAt: createdAt,
	}, "rolling-stock.json", RollingStockDocument{Locomotives: items})
}

// BuildLayoutArchive creates an importable archive without requiring a store.
// Runtime turnout and block state are omitted by LayoutDocument.MarshalJSON.
func BuildLayoutArchive(createdAt time.Time, layout model.LayoutDefinition) ([]byte, error) {
	if err := validateLayout(&layout); err != nil {
		return nil, err
	}
	if layout.Presentation != nil {
		if err := model.ValidateLayoutPresentation(*layout.Presentation, layout); err != nil {
			return nil, err
		}
	}
	return writeArchive(Manifest{
		Format: FormatID, Version: LayoutFormatVersion, PackageType: "layout", CreatedAt: createdAt,
	}, "layout.json", LayoutDocument{Layout: layout})
}

func (s *Service) ExportLayout(ctx context.Context) ([]byte, error) {
	layout, err := s.store.ExportLayout(ctx)
	if err != nil {
		return nil, err
	}
	return writeArchive(Manifest{Format: FormatID, Version: LayoutFormatVersion, PackageType: "layout", CreatedAt: s.clock.Now()}, "layout.json", LayoutDocument{Layout: layout})
}

func (s *Service) ImportRollingStock(ctx context.Context, user model.User, data []byte, replace bool) error {
	if !service.Allowed(user.Role, service.PermissionConfigure) {
		return service.ErrPermissionDenied
	}
	var doc RollingStockDocument
	if err := readArchive(data, "rolling-stock", "rolling-stock.json", &doc); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidArchive, err)
	}
	if err := validateLocomotives(doc.Locomotives); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidArchive, err)
	}
	if err := s.store.ReplaceLocomotives(ctx, doc.Locomotives, replace); err != nil {
		return err
	}
	s.events.Publish("rolling-stock.imported", map[string]any{"count": len(doc.Locomotives), "replace": replace, "userId": user.ID})
	return nil
}
func (s *Service) ImportLayout(ctx context.Context, user model.User, data []byte, replace bool) error {
	if !service.Allowed(user.Role, service.PermissionConfigure) {
		return service.ErrPermissionDenied
	}
	var doc LayoutDocument
	if err := readArchive(data, "layout", "layout.json", &doc); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidArchive, err)
	}
	if err := validateLayout(&doc.Layout); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidArchive, err)
	}
	if err := s.store.ImportLayout(ctx, doc.Layout, replace); err != nil {
		if errors.Is(err, model.ErrInvalidLayoutPresentation) {
			return fmt.Errorf("%w: %v", ErrInvalidArchive, err)
		}
		return err
	}
	s.events.Publish("layout.imported", map[string]any{"blocks": len(doc.Layout.Blocks), "turnouts": len(doc.Layout.Turnouts), "routes": len(doc.Layout.Routes), "replace": replace, "userId": user.ID})
	return nil
}

func writeArchive(manifest Manifest, name string, doc any) ([]byte, error) {
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for _, entry := range []struct {
		filename string
		value    any
	}{{"manifest.json", manifest}, {name, doc}} {
		w, err := zw.Create(entry.filename)
		if err != nil {
			return nil, err
		}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if err := enc.Encode(entry.value); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
func readArchive(data []byte, packageType, documentName string, target any) error {
	if len(data) == 0 || len(data) > MaxArchiveSize {
		return errors.New("archive size is invalid")
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return fmt.Errorf("invalid ZIP archive: %w", err)
	}
	files := map[string]*zip.File{}
	for _, f := range zr.File {
		clean := strings.TrimPrefix(f.Name, "./")
		if strings.Contains(clean, "..") || strings.HasPrefix(clean, "/") {
			return errors.New("unsafe archive path")
		}
		if f.UncompressedSize64 > MaxEntrySize {
			return errors.New("archive entry too large")
		}
		files[clean] = f
	}
	manifestFile := files["manifest.json"]
	docFile := files[documentName]
	if manifestFile == nil || docFile == nil {
		return errors.New("archive is missing manifest or document")
	}
	var manifest Manifest
	if err := decodeZipJSON(manifestFile, &manifest); err != nil {
		return err
	}
	maxVersion := FormatVersion
	if packageType == "layout" {
		maxVersion = LayoutFormatVersion
	}
	if manifest.Format != FormatID || manifest.Version < OldestVersion || manifest.Version > maxVersion || manifest.PackageType != packageType {
		return fmt.Errorf("unsupported archive format %q version %d type %q", manifest.Format, manifest.Version, manifest.PackageType)
	}
	return decodeZipJSON(docFile, target)
}
func decodeZipJSON(file *zip.File, target any) error {
	r, err := file.Open()
	if err != nil {
		return err
	}
	defer r.Close()
	dec := json.NewDecoder(io.LimitReader(r, MaxEntrySize))
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return fmt.Errorf("invalid %s: %w", file.Name, err)
	}
	return nil
}
func validateLocomotives(items []model.Locomotive) error {
	seen := map[string]bool{}
	for i, x := range items {
		if x.ID == "" || x.Name == "" {
			return fmt.Errorf("locomotive %d requires id and name", i)
		}
		if seen[x.ID] {
			return fmt.Errorf("duplicate locomotive id %q", x.ID)
		}
		seen[x.ID] = true
		if x.DCCAddress < 1 || x.DCCAddress > 9999 {
			return fmt.Errorf("locomotive %q has invalid DCC address", x.ID)
		}
		if x.AddressKind != "short" && x.AddressKind != "long" {
			return fmt.Errorf("locomotive %q has invalid address kind", x.ID)
		}
		if x.SpeedSteps != 14 && x.SpeedSteps != 28 && x.SpeedSteps != 128 {
			return fmt.Errorf("locomotive %q has invalid speed steps", x.ID)
		}
	}
	return nil
}
func validateLayout(layout *model.LayoutDefinition) error {
	if layout == nil {
		return errors.New("layout is required")
	}
	blocks := map[string]bool{}
	turnouts := map[string]model.Turnout{}
	routes := map[string]bool{}
	for _, b := range layout.Blocks {
		blocks[b.ID] = true
	}
	for i, t := range layout.Turnouts {
		normalized, err := model.NormalizeTurnout(t)
		if err != nil {
			return err
		}
		if _, exists := turnouts[normalized.ID]; exists {
			return fmt.Errorf("duplicate turnout %q", t.ID)
		}
		layout.Turnouts[i] = normalized
		turnouts[normalized.ID] = normalized
	}
	graph, err := topology.Build(*layout)
	if err != nil {
		return err
	}
	for _, r := range layout.Routes {
		if r.ID == "" || r.Name == "" {
			return errors.New("every route requires id and name")
		}
		if routes[r.ID] {
			return fmt.Errorf("duplicate route %q", r.ID)
		}
		routes[r.ID] = true
	}
	for _, m := range layout.FeedbackMappings {
		if m.Provider == "" || m.Address < 0 || !blocks[m.BlockID] {
			return fmt.Errorf("invalid feedback mapping %s:%d", m.Provider, m.Address)
		}
	}
	providers := make(map[string]bool, len(layout.OccupancyProviders))
	for _, provider := range layout.OccupancyProviders {
		if providers[provider.ID] {
			return fmt.Errorf("duplicate occupancy provider %q", provider.ID)
		}
		if err := model.ValidateOccupancyProvider(provider); err != nil {
			return err
		}
		providers[provider.ID] = true
	}
	mappings := make(map[string]bool, len(layout.OccupancySensorMappings))
	for _, mapping := range layout.OccupancySensorMappings {
		if err := model.ValidateOccupancySensorMapping(mapping); err != nil {
			return err
		}
		if !providers[mapping.ProviderID] {
			return fmt.Errorf("occupancy mapping %s/%s references unknown provider", mapping.ProviderID, mapping.SensorID)
		}
		if !blocks[mapping.BlockID] {
			return fmt.Errorf("occupancy mapping %s/%s references unknown block %q", mapping.ProviderID, mapping.SensorID, mapping.BlockID)
		}
		key := mapping.ProviderID + "\x00" + mapping.SensorID
		if mappings[key] {
			return fmt.Errorf("duplicate occupancy mapping %s/%s", mapping.ProviderID, mapping.SensorID)
		}
		mappings[key] = true
	}
	for _, r := range layout.Routes {
		for _, id := range r.BlockIDs {
			if !blocks[id] {
				return fmt.Errorf("route %q references unknown block %q", r.ID, id)
			}
		}
		for id, state := range r.TurnoutStates {
			turnout, exists := turnouts[id]
			if !exists {
				return fmt.Errorf("route %q references unknown turnout %q", r.ID, id)
			}
			if _, exists := turnout.Position(state); !exists {
				return fmt.Errorf("route %q references unknown position %q on turnout %q", r.ID, state, id)
			}
		}
		for _, id := range r.ConflictRouteIDs {
			if !routes[id] {
				return fmt.Errorf("route %q references unknown conflict %q", r.ID, id)
			}
		}
	}
	if err := topology.RouteValidationErrors(topology.ValidateRouteDefinitions(graph, layout.Routes, layout.Turnouts)); err != nil {
		return err
	}
	return nil
}
