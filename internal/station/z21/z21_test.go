package z21

import (
	"bytes"
	"testing"
)

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
	d := New("unused")
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
}
