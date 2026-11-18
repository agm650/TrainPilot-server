package dccex

import (
	"context"
	"fmt"
	"net"
	"strings"

	"github.com/agm650/TrainPilot-server/internal/station"
)

// Initial verified target. Other versions keep binary control but must be
// reviewed before extended support or wider aspect limits are advertised.
const verifiedExtendedFirmware = "5.6.6"

var _ station.ExtendedAccessoryController = (*Driver)(nil)
var _ station.StatusProvider = (*Driver)(nil)

// Firmware 5.6.6 accepts five-bit aspect values. Its parser applies the RCN-213
// address offset itself; the public linear address is sent unchanged.
func extendedAccessoryCommand(command station.ExtendedAccessoryCommand) (string, error) {
	if err := command.Validate(); err != nil {
		return "", err
	}
	if command.Aspect > 31 {
		return "", station.ErrInvalidExtendedAccessoryAspect
	}
	return fmt.Sprintf("<A %d %d>\n", command.Address, command.Aspect), nil
}

func (d *Driver) SetExtendedAccessory(ctx context.Context, command station.ExtendedAccessoryCommand) error {
	encoded, err := extendedAccessoryCommand(command)
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
	bounded, cancel := context.WithTimeout(ctx, defaultDialTimeout)
	defer cancel()
	d.mu.Lock()
	c, id := d.conn, d.connectionID
	d.mu.Unlock()
	if c == nil {
		return station.ErrOffline
	}
	// There is no correlated acknowledgment in this protocol. A successful
	// write updates commanded state only, never a station or physical report.
	return d.sendOnConnection(bounded, encoded, c, id)
}

func (d *Driver) startDiscovery(c net.Conn, id uint64) {
	d.mu.Lock()
	if d.closed || d.conn != c || d.connectionID != id {
		d.mu.Unlock()
		return
	}
	d.wg.Add(1)
	d.mu.Unlock()
	go func() {
		defer d.wg.Done()
		ctx, cancel := context.WithTimeout(d.runCtx, defaultDialTimeout)
		defer cancel()
		_ = d.sendOnConnection(ctx, "<s>\n", c, id)
	}()
}

func (d *Driver) currentStatus() station.Status {
	d.mu.Lock()
	power := d.trackPower
	d.mu.Unlock()
	if power == "" {
		power = "unknown"
	}
	health := d.health.Health()
	return station.Status{Connectivity: health.Connectivity, LastSeen: health.LastSeen, TrackPower: power}
}

func (d *Driver) Status(ctx context.Context) (station.Status, error) {
	if err := ctx.Err(); err != nil {
		return d.currentStatus(), err
	}
	return d.currentStatus(), nil
}

// The 5.6.6 status query returns an identity frame and power broadcasts. A
// per-track OFF is conservative until the aggregate MAIN status follows.
// PROG alone and per-track ON never establish that the main track is powered.
func (d *Driver) handleControlFrame(frame string, connectionID uint64) bool {
	if strings.HasPrefix(frame, "<iDCC-EX V-") {
		fields := strings.Fields(strings.TrimSuffix(strings.TrimPrefix(frame, "<iDCC-EX V-"), ">"))
		if len(fields) == 0 {
			return true
		}
		d.mu.Lock()
		defer d.mu.Unlock()
		if !d.closed && d.conn != nil && d.connectionID == connectionID {
			d.firmware = fields[0]
		}
		return true
	}
	if !strings.HasPrefix(frame, "<p") {
		return false
	}
	fields := strings.Fields(strings.TrimSuffix(strings.TrimPrefix(frame, "<p"), ">"))
	if len(fields) == 0 || len(fields) > 2 {
		return true
	}
	power := ""
	switch {
	case len(fields) == 1 && fields[0] == "0":
		power = "off"
	case len(fields) == 1 && fields[0] == "1":
		power = "on"
	case len(fields) == 1 && fields[0] == "2":
		power = "unknown"
	case len(fields) == 2 && (fields[1] == "MAIN" || fields[1] == "JOIN"):
		if fields[0] == "1" {
			power = "on"
		} else if fields[0] == "0" {
			power = "off"
		}
	case len(fields) == 2 && fields[0] == "0" && len(fields[1]) == 1 && fields[1][0] >= 'A' && fields[1][0] <= 'Z':
		power = "unknown"
	}
	if power == "" {
		return true
	}
	d.mu.Lock()
	if d.closed || d.conn == nil || d.connectionID != connectionID {
		d.mu.Unlock()
		return true
	}
	changed := d.trackPower != power
	d.trackPower = power
	d.mu.Unlock()
	if changed {
		d.publishHealthStatus()
	}
	return true
}
