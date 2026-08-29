package websocket

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const websocketGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

type Conn struct {
	conn net.Conn
	r    *bufio.Reader
	w    *bufio.Writer
	mu   sync.Mutex
}

func Accept(w http.ResponseWriter, r *http.Request) (*Conn, error) {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") || !headerContains(r.Header.Get("Connection"), "upgrade") {
		return nil, errors.New("websocket upgrade required")
	}
	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" || r.Header.Get("Sec-WebSocket-Version") != "13" {
		return nil, errors.New("invalid websocket headers")
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		return nil, errors.New("HTTP server does not support hijacking")
	}
	conn, rw, err := hijacker.Hijack()
	if err != nil {
		return nil, err
	}
	h := sha1.Sum([]byte(key + websocketGUID))
	accept := base64.StdEncoding.EncodeToString(h[:])
	if _, err := fmt.Fprintf(rw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", accept); err != nil {
		conn.Close()
		return nil, err
	}
	if err := rw.Flush(); err != nil {
		conn.Close()
		return nil, err
	}
	return &Conn{conn: conn, r: rw.Reader, w: rw.Writer}, nil
}

func headerContains(value, token string) bool {
	for _, part := range strings.Split(value, ",") {
		if strings.EqualFold(strings.TrimSpace(part), token) {
			return true
		}
	}
	return false
}

func (c *Conn) Close() error {
	_ = c.conn.SetWriteDeadline(time.Now().Add(250 * time.Millisecond))
	_ = c.writeFrame(0x8, []byte{0x03, 0xE8})
	return c.conn.Close()
}

func (c *Conn) WriteJSON(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.writeFrame(0x1, b)
}

func (c *Conn) SetWriteDeadline(deadline time.Time) error {
	return c.conn.SetWriteDeadline(deadline)
}

func (c *Conn) ReadJSON(v any) error {
	for {
		opcode, payload, err := c.readFrame()
		if err != nil {
			return err
		}
		switch opcode {
		case 0x1:
			return json.Unmarshal(payload, v)
		case 0x8:
			return io.EOF
		case 0x9:
			if err := c.writeFrame(0xA, payload); err != nil {
				return err
			}
		case 0xA:
			continue
		default:
			return fmt.Errorf("unsupported websocket opcode %d", opcode)
		}
	}
}

func (c *Conn) writeFrame(opcode byte, payload []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(payload) > 1<<20 {
		return errors.New("websocket payload too large")
	}
	if err := c.w.WriteByte(0x80 | opcode); err != nil {
		return err
	}
	switch n := len(payload); {
	case n < 126:
		if err := c.w.WriteByte(byte(n)); err != nil {
			return err
		}
	case n <= 65535:
		if err := c.w.WriteByte(126); err != nil {
			return err
		}
		var b [2]byte
		binary.BigEndian.PutUint16(b[:], uint16(n))
		if _, err := c.w.Write(b[:]); err != nil {
			return err
		}
	default:
		if err := c.w.WriteByte(127); err != nil {
			return err
		}
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], uint64(n))
		if _, err := c.w.Write(b[:]); err != nil {
			return err
		}
	}
	if _, err := c.w.Write(payload); err != nil {
		return err
	}
	return c.w.Flush()
}

func (c *Conn) readFrame() (byte, []byte, error) {
	first, err := c.r.ReadByte()
	if err != nil {
		return 0, nil, err
	}
	second, err := c.r.ReadByte()
	if err != nil {
		return 0, nil, err
	}
	if first&0x80 == 0 {
		return 0, nil, errors.New("fragmented websocket frames are not supported")
	}
	opcode := first & 0x0F
	masked := second&0x80 != 0
	if !masked {
		return 0, nil, errors.New("client websocket frame must be masked")
	}
	length := uint64(second & 0x7F)
	if length == 126 {
		var b [2]byte
		if _, err := io.ReadFull(c.r, b[:]); err != nil {
			return 0, nil, err
		}
		length = uint64(binary.BigEndian.Uint16(b[:]))
	}
	if length == 127 {
		var b [8]byte
		if _, err := io.ReadFull(c.r, b[:]); err != nil {
			return 0, nil, err
		}
		length = binary.BigEndian.Uint64(b[:])
	}
	if length > 1<<20 {
		return 0, nil, errors.New("websocket payload too large")
	}
	var mask [4]byte
	if _, err := io.ReadFull(c.r, mask[:]); err != nil {
		return 0, nil, err
	}
	payload := make([]byte, int(length))
	if _, err := io.ReadFull(c.r, payload); err != nil {
		return 0, nil, err
	}
	for i := range payload {
		payload[i] ^= mask[i%4]
	}
	return opcode, payload, nil
}
