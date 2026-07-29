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
