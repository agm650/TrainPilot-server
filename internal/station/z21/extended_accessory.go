package z21

import (
	"context"
	"net"
	"time"

	"github.com/agm650/TrainPilot-server/internal/station"
)

const (
	lanXSetExtendedAccessory     = 0x54
	lanXGetExtendedAccessoryInfo = 0x44
)

var _ station.ExtendedAccessoryController = (*Driver)(nil)
var _ station.ExtendedAccessoryStateEventProvider = (*Driver)(nil)

// Extended addressing follows RCN-213: displayed address 1 is raw address 4.
// It must not use the basic accessory offset or turnout activation pulses.
func extendedRawAddress(address int) (uint16, error) {
	if address < station.MinExtendedAccessoryAddress || address > station.MaxExtendedAccessoryAddress {
		return 0, station.ErrInvalidExtendedAccessoryAddress
	}
	return uint16(address + 3), nil
}

func extendedAccessoryPacket(command station.ExtendedAccessoryCommand) ([]byte, error) {
	if err := command.Validate(); err != nil {
		return nil, err
	}
	raw, _ := extendedRawAddress(command.Address)
	return xbus(lanXSetExtendedAccessory, byte(raw>>8), byte(raw), byte(command.Aspect), 0), nil
}

func extendedInfoPacket(address int) ([]byte, error) {
	raw, err := extendedRawAddress(address)
	if err != nil {
		return nil, err
	}
	return xbus(lanXGetExtendedAccessoryInfo, byte(raw>>8), byte(raw), 0), nil
}

// Success is transport dispatch, not an acknowledgment. GET only requests the
// station's last-command observation; a missing report remains unknown.
func (d *Driver) SetExtendedAccessory(ctx context.Context, command station.ExtendedAccessoryCommand) error {
	packet, err := extendedAccessoryPacket(command)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !d.Capabilities().ExtendedAccessoryControl {
		return station.ErrUnsupported
	}
	if d.Health().Connectivity != station.Online {
		return station.ErrOffline
	}
	if err := d.send(ctx, packet); err != nil {
		return err
	}
	query, _ := extendedInfoPacket(command.Address)
	return d.send(ctx, query)
}

func (d *Driver) ExtendedAccessoryStateEvents() <-chan station.ExtendedAccessoryStateEvent {
	return d.extendedEvents
}

func (d *Driver) observeFirmware(payload []byte, c *net.UDPConn, generation, powerGeneration uint64) {
	for _, b := range payload[2:4] {
		if b&0x0f > 9 || b>>4 > 9 {
			return
		}
	}
	value := uint16(payload[2])<<8 | uint16(payload[3])
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || d.conn != c || d.feedbackGeneration != generation || d.feedbackPowerGeneration != powerGeneration {
		return
	}
	d.firmware = value
}

func (d *Driver) parseExtendedAccessoryInfo(payload []byte, c *net.UDPConn, generation, powerGeneration uint64) {
	raw := int(payload[1])<<8 | int(payload[2])
	address := raw - 3
	if _, err := extendedRawAddress(address); err != nil {
		return
	}
	e := station.ExtendedAccessoryStateEvent{Address: address, Quality: station.AccessoryReportStation, ObservedAt: time.Now()}
	switch payload[4] {
	case 0:
		aspect := int(payload[3])
		e.Aspect = &aspect
		e.State = station.AccessoryReportKnown
	case 0xff:
		e.State = station.AccessoryReportUnknown
	default:
		e.State = station.AccessoryReportInvalid
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || d.conn != c || d.feedbackGeneration != generation || d.feedbackPowerGeneration != powerGeneration {
		return
	}
	// Bounded best-effort observations, keeping the newest report on overflow.
	// Explicit commands request a fresh station report; no command is replayed.
	select {
	case d.extendedEvents <- e:
	default:
		select {
		case <-d.extendedEvents:
		default:
		}
		select {
		case d.extendedEvents <- e:
		default:
		}
	}
}

// Caller holds mu. Retired connections must not leave queued observations or
// a firmware capability that could be attributed to their replacement.
func (d *Driver) invalidateExtendedLocked() {
	d.firmware = 0
	for {
		select {
		case <-d.extendedEvents:
		default:
			return
		}
	}
}
