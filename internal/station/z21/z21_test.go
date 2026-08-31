package z21

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/agm650/TrainPilot-server/internal/station"
)

func TestCapabilitiesAndCommandBounds(t *testing.T) {
	d := New("unused", station.DefaultOfflineAfter)
	caps := d.Capabilities()
	if caps.Functions != 29 || caps.MaxFunctionNumber != 28 {
		t.Fatalf("capabilities=%+v", caps)
	}
	if err := d.SetLocoSpeed(context.Background(), 3, 0.5, station.Direction("sideways")); err != station.ErrUnsupported {
		t.Fatalf("invalid direction error=%v", err)
	}
	if err := d.SetLocoFunction(context.Background(), 3, 29, true); err != station.ErrUnsupported {
		t.Fatalf("out-of-range function error=%v", err)
	}
}

func TestBasicAccessoryValidationAndOffline(t *testing.T) {
	driver := New("unused", station.DefaultOfflineAfter)
	if err := driver.SetBasicAccessory(context.Background(), station.AccessoryCommand{Address: 0, Position: station.AccessoryPosition1}); !errors.Is(err, station.ErrInvalidAccessoryAddress) {
		t.Fatalf("invalid address error=%v", err)
	}
	if err := driver.SetBasicAccessory(context.Background(), station.AccessoryCommand{Address: 1, Position: station.AccessoryPosition("invalid")}); !errors.Is(err, station.ErrInvalidAccessoryPosition) {
		t.Fatalf("invalid position error=%v", err)
	}
	if err := driver.SetBasicAccessory(context.Background(), station.AccessoryCommand{Address: 1, Position: station.AccessoryPosition1}); !errors.Is(err, station.ErrOffline) {
		t.Fatalf("offline accessory error=%v", err)
	}
	driver.health.Connected()
	driver.health.ValidResponse()
	if err := driver.SetBasicAccessory(context.Background(), station.AccessoryCommand{Address: 1, Position: station.AccessoryPosition1}); !errors.Is(err, station.ErrUnsupported) {
		t.Fatalf("online accessory error=%v", err)
	}
}

func TestTrackPowerPacket(t *testing.T) {
	on := xbus(0x21, 0x81)
	want := []byte{0x07, 0x00, 0x40, 0x00, 0x21, 0x81, 0xA0}
	if !bytes.Equal(on, want) {
		t.Fatalf("packet=% x want=% x", on, want)
	}
	off := xbus(0x21, 0x80)
	want = []byte{0x07, 0x00, 0x40, 0x00, 0x21, 0x80, 0xA1}
	if !bytes.Equal(off, want) {
		t.Fatalf("packet=% x want=% x", off, want)
	}
}

func TestStatusRequestPackets(t *testing.T) {
	if got, want := xbus(0x21, 0x24), []byte{0x07, 0x00, 0x40, 0x00, 0x21, 0x24, 0x05}; !bytes.Equal(got, want) {
		t.Fatalf("LAN_X_GET_STATUS=% x want=% x", got, want)
	}
	if got, want := lan(0x0085), []byte{0x04, 0x00, 0x85, 0x00}; !bytes.Equal(got, want) {
		t.Fatalf("LAN_SYSTEMSTATE_GETDATA=% x want=% x", got, want)
	}
}

func TestParseSystemState(t *testing.T) {
	payload := []byte{
		0x7b, 0x00, 0xf4, 0xff, 0x64, 0x00, 0x2a, 0x00,
		0x10, 0x27, 0xe0, 0x2e, 0x27, 0x0b, 0x00, 0x39,
	}
	status := statusFromSystemState(parseSystemState(payload))
	if status.TrackPower != "off" || !status.EmergencyStop || !status.ShortCircuit || !status.ProgrammingMode {
		t.Fatalf("central status=%+v", status)
	}
	if status.MainCurrentMilliAmps != 123 || status.ProgrammingCurrentMilliAmps != -12 || status.FilteredMainCurrentMilliAmps != 100 || status.TemperatureCelsius != 42 {
		t.Fatalf("measurements=%+v", status)
	}
	if status.SupplyVoltageMilliVolts != 10000 || status.TrackVoltageMilliVolts != 12000 || !status.HighTemperature || !status.PowerLost || !status.InternalShortCircuit {
		t.Fatalf("extended status=%+v", status)
	}
}

func TestParseDispatchesStatusReplies(t *testing.T) {
	d := New("unused", station.DefaultOfflineAfter)
	d.health.Connected()
	xReply := make(chan byte, 1)
	systemReply := make(chan systemState, 1)
	d.xStatusWaiters = append(d.xStatusWaiters, xReply)
	d.systemStateWaiters = append(d.systemStateWaiters, systemReply)
	xPayload := []byte{0x62, 0x22, 0x02}
	xRecord := xbus(xPayload...)
	systemRecord := append(lan(0x0084), make([]byte, 16)...)
	systemRecord[0] = 20
	d.parse(append(xRecord, systemRecord...))
	if got := <-xReply; got != 0x02 {
		t.Fatalf("x status=%x", got)
	}
	select {
	case <-systemReply:
	default:
		t.Fatal("system state was not dispatched")
	}
	if health := d.Health(); health.Connectivity != station.Online || health.LastSeen == nil {
		t.Fatalf("health after valid replies=%+v", health)
	}
}

func TestInvalidXBusChecksumDoesNotRestoreHealth(t *testing.T) {
	d := New("unused", station.DefaultOfflineAfter)
	d.health.Connected()
	d.health.CommunicationError()
	record := []byte{0x08, 0x00, 0x40, 0x00, 0x62, 0x22, 0x02, 0x00}
	d.parse(record)
	if got := d.Health().Connectivity; got != station.Degraded {
		t.Fatalf("health=%s want degraded", got)
	}
}
