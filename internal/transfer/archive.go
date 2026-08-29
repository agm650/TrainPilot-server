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
)

const (
	FormatID       = "org.dcc-control.package"
	FormatVersion  = 1
	MaxArchiveSize = 25 << 20
	MaxEntrySize   = 10 << 20
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
func (s *Service) ExportLayout(ctx context.Context) ([]byte, error) {
	layout, err := s.store.ExportLayout(ctx)
	if err != nil {
		return nil, err
	}
	return writeArchive(Manifest{Format: FormatID, Version: FormatVersion, PackageType: "layout", CreatedAt: s.clock.Now()}, "layout.json", LayoutDocument{Layout: layout})
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
	if err := validateLayout(doc.Layout); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidArchive, err)
	}
	if err := s.store.ImportLayout(ctx, doc.Layout, replace); err != nil {
		return err
	}
	s.events.Publish("layout.imported", map[string]any{"blocks": len(doc.Layout.Blocks), "turnouts": len(doc.Layout.Turnouts), "routes": len(doc.Layout.Routes), "replace": replace, "userId": user.ID})
	return nil
}

func writeArchive(manifest Manifest, name string, doc any) ([]byte, error) {
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for filename, value := range map[string]any{"manifest.json": manifest, name: doc} {
		w, err := zw.Create(filename)
		if err != nil {
			return nil, err
		}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if err := enc.Encode(value); err != nil {
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
	if manifest.Format != FormatID || manifest.Version != FormatVersion || manifest.PackageType != packageType {
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
func validateLayout(layout model.LayoutDefinition) error {
	blocks := map[string]bool{}
	turnouts := map[string]bool{}
	routes := map[string]bool{}
	for _, b := range layout.Blocks {
		if b.ID == "" || b.Name == "" {
			return errors.New("every block requires id and name")
		}
		if blocks[b.ID] {
			return fmt.Errorf("duplicate block %q", b.ID)
		}
		blocks[b.ID] = true
	}
	for _, t := range layout.Turnouts {
		if t.ID == "" || t.Name == "" || t.DCCAddress < 1 {
			return fmt.Errorf("invalid turnout %q", t.ID)
		}
		if turnouts[t.ID] {
			return fmt.Errorf("duplicate turnout %q", t.ID)
		}
		turnouts[t.ID] = true
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
	for _, r := range layout.Routes {
		for _, id := range r.BlockIDs {
			if !blocks[id] {
				return fmt.Errorf("route %q references unknown block %q", r.ID, id)
			}
		}
		for id, state := range r.TurnoutStates {
			if !turnouts[id] {
				return fmt.Errorf("route %q references unknown turnout %q", r.ID, id)
			}
			if state != "straight" && state != "diverging" {
				return fmt.Errorf("route %q has invalid turnout state", r.ID)
			}
		}
		for _, id := range r.ConflictRouteIDs {
			if !routes[id] {
				return fmt.Errorf("route %q references unknown conflict %q", r.ID, id)
			}
		}
	}
	return nil
}
