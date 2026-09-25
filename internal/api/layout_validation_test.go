package api

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/agm650/TrainPilot-server/internal/model"
	"github.com/agm650/TrainPilot-server/internal/model/topologyfixture"
	"github.com/agm650/TrainPilot-server/internal/station/simulator"
	"github.com/agm650/TrainPilot-server/internal/transfer"
)

func postLayoutValidation(t *testing.T, endpoint, token string, archive []byte) (int, transfer.LayoutValidationResult) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, endpoint+"/api/v1/layout/validate?mode=replace", bytes.NewReader(archive))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", archiveContentType)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var result transfer.LayoutValidationResult
	if response.StatusCode == http.StatusOK {
		if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
	}
	return response.StatusCode, result
}

func invalidTrackPathArchive(t *testing.T, archive []byte) []byte {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	for _, file := range reader.File {
		entry, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(entry)
		entry.Close()
		if err != nil {
			t.Fatal(err)
		}
		if file.Name == "layout.json" {
			var document map[string]any
			if err := json.Unmarshal(data, &document); err != nil {
				t.Fatal(err)
			}
			layout := document["layout"].(map[string]any)
			presentation := layout["presentation"].(map[string]any)
			path := presentation["trackSections"].([]any)[0].(map[string]any)
			segment := path["segments"].([]any)[0].(map[string]any)
			segment["to"].(map[string]any)["x"] = 999
			data, err = json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
		}
		destination, err := writer.Create(file.Name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := destination.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func TestLayoutValidationHTTPIsAdministratorOnlyAndSideEffectFree(t *testing.T) {
	ctx := context.Background()
	fixture := newDetailedHTTPFixture(t)
	layout := topologyfixture.PassingStation()
	p := model.EmptyLayoutPresentation()
	p.Nodes = []model.LayoutNodePosition{{NodeID: "west-boundary", X: 0, Y: 0}, {NodeID: "station-west-stem", X: 100, Y: 0}}
	p.TrackSections = []model.LayoutTrackPath{{TrackSectionID: "approach-west", Segments: []model.LayoutTrackSegment{{Type: "line", To: &model.LayoutPoint{X: 100, Y: 0}}}}}
	p.Blocks = []model.LayoutBlockStyle{{BlockID: "block-west", Color: "#123456", Opacity: 1}}
	layout.Presentation = &p
	archive, err := transfer.BuildLayoutArchive(time.Now(), layout)
	if err != nil {
		t.Fatal(err)
	}
	before, err := fixture.db.ExportLayout(ctx)
	if err != nil {
		t.Fatal(err)
	}
	topologyBefore, err := fixture.viewer.Topology(ctx)
	if err != nil {
		t.Fatal(err)
	}
	presentationBefore, err := fixture.viewer.LayoutPresentation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, operation := range []simulator.Operation{simulator.OpTrackPower, simulator.OpEmergencyStop, simulator.OpThrottle, simulator.OpFunction, simulator.OpAccessory} {
		if err := fixture.simulator.SetOperationFault(operation, simulator.OperationFault{Every: 1000}); err != nil {
			t.Fatal(err)
		}
	}
	stationBefore := fixture.simulator.Snapshot()
	stream, unsubscribe := fixture.bus.Subscribe(2)
	defer unsubscribe()

	if status, _ := postLayoutValidation(t, fixture.server.URL, "", archive); status != http.StatusUnauthorized {
		t.Fatalf("anonymous validation status=%d", status)
	}
	if status, _ := postLayoutValidation(t, fixture.server.URL, fixture.viewer.AccessToken, archive); status != http.StatusForbidden {
		t.Fatalf("viewer validation status=%d", status)
	}
	status, valid := postLayoutValidation(t, fixture.server.URL, fixture.administrator.AccessToken, archive)
	if status != http.StatusOK || !valid.Valid || len(valid.Errors) != 0 || valid.Warnings == nil {
		t.Fatalf("valid archive response=%d %+v", status, valid)
	}
	status, invalid := postLayoutValidation(t, fixture.server.URL, fixture.administrator.AccessToken, invalidTrackPathArchive(t, archive))
	if status != http.StatusOK || invalid.Valid || len(invalid.Errors) != 1 || invalid.Errors[0].Code != "layout_track_path_invalid" || invalid.Errors[0].ResourceType != "trackSection" || invalid.Errors[0].ResourceID != "approach-west" {
		t.Fatalf("invalid archive response=%d %+v", status, invalid)
	}
	after, err := fixture.db.ExportLayout(ctx)
	if err != nil || !reflect.DeepEqual(after, before) {
		t.Fatalf("validation changed persisted layout: %v", err)
	}
	topologyAfter, err := fixture.viewer.Topology(ctx)
	if err != nil || topologyBefore.Revision != topologyAfter.Revision {
		t.Fatalf("topology revision changed: %v", err)
	}
	presentationAfter, err := fixture.viewer.LayoutPresentation(ctx)
	if err != nil || presentationBefore.Revision != presentationAfter.Revision {
		t.Fatalf("presentation revision changed: %v", err)
	}
	stationAfter := fixture.simulator.Snapshot()
	if !reflect.DeepEqual(stationBefore.OperationFaults, stationAfter.OperationFaults) || !reflect.DeepEqual(stationBefore.Accessories, stationAfter.Accessories) || !reflect.DeepEqual(stationBefore.Locomotives, stationAfter.Locomotives) || stationBefore.TrackPower != stationAfter.TrackPower {
		t.Fatal("validation issued a simulator command")
	}
	select {
	case event := <-stream:
		t.Fatalf("validation published event: %+v", event)
	default:
	}
}
