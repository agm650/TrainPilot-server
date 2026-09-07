package benchmark

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	webSocketGUID       = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
	maxWebSocketPayload = 4 << 20
)

type webSocketClient struct {
	conn    net.Conn
	reader  *bufio.Reader
	writeMu sync.Mutex
}

func dialWebSocket(ctx context.Context, serverURL, accessToken string, timeout time.Duration) (*webSocketClient, error) {
	parsed, err := url.Parse(serverURL)
	if err != nil {
		return nil, fmt.Errorf("parse server URL: %w", err)
	}
	secure := false
	switch parsed.Scheme {
	case "http":
		parsed.Scheme = "ws"
	case "https":
		parsed.Scheme = "wss"
		secure = true
	case "ws":
	case "wss":
		secure = true
	default:
		return nil, fmt.Errorf("unsupported WebSocket scheme %q", parsed.Scheme)
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/api/v1/events"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	host := parsed.Host
	if parsed.Port() == "" {
		if secure {
			host = net.JoinHostPort(parsed.Hostname(), "443")
		} else {
			host = net.JoinHostPort(parsed.Hostname(), "80")
		}
	}
	dialer := &net.Dialer{Timeout: timeout}
	var conn net.Conn
	if secure {
		hostname := parsed.Hostname()
		conn, err = tls.DialWithDialer(dialer, "tcp", host, &tls.Config{ServerName: hostname, MinVersion: tls.VersionTLS12})
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", host)
	}
	if err != nil {
		return nil, fmt.Errorf("dial WebSocket: %w", err)
	}
	failed := true
	defer func() {
		if failed {
			_ = conn.Close()
		}
	}()
	keyBytes := make([]byte, 16)
	if _, err := rand.Read(keyBytes); err != nil {
		return nil, err
	}
	key := base64.StdEncoding.EncodeToString(keyBytes)
	request := fmt.Sprintf("GET %s HTTP/1.1\r\nHost: %s\r\nAuthorization: Bearer %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n\r\n", parsed.RequestURI(), parsed.Host, accessToken, key)
	_ = conn.SetDeadline(time.Now().Add(timeout))
	if _, err := io.WriteString(conn, request); err != nil {
		return nil, err
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodGet})
	if err != nil {
		return nil, fmt.Errorf("read WebSocket handshake: %w", err)
	}
	if response.StatusCode != http.StatusSwitchingProtocols {
		return nil, fmt.Errorf("WebSocket upgrade returned %s", response.Status)
	}
	expectedHash := sha1.Sum([]byte(key + webSocketGUID))
	expectedAccept := base64.StdEncoding.EncodeToString(expectedHash[:])
	if response.Header.Get("Sec-WebSocket-Accept") != expectedAccept {
		return nil, errors.New("invalid Sec-WebSocket-Accept response")
	}
	_ = conn.SetDeadline(time.Time{})
	failed = false
	return &webSocketClient{conn: conn, reader: reader}, nil
}

func (c *webSocketClient) Close() error {
	_ = c.conn.SetWriteDeadline(time.Now().Add(250 * time.Millisecond))
	_ = c.writeFrame(0x8, []byte{0x03, 0xe8})
	return c.conn.Close()
}

func (c *webSocketClient) SetReadDeadline(deadline time.Time) error {
	return c.conn.SetReadDeadline(deadline)
}

func (c *webSocketClient) WriteJSON(value any, timeout time.Duration) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_ = c.conn.SetWriteDeadline(time.Now().Add(timeout))
	err = c.writeFrame(0x1, payload)
	if err == nil {
		_ = c.conn.SetWriteDeadline(time.Time{})
	}
	return err
}

func (c *webSocketClient) ReadJSON(target any) error {
	for {
		opcode, payload, err := c.readFrame()
		if err != nil {
			return err
		}
		switch opcode {
		case 0x1:
			return json.Unmarshal(payload, target)
		case 0x8:
			return io.EOF
		case 0x9:
			if err := c.writeFrame(0xA, payload); err != nil {
				return err
			}
		case 0xA:
			continue
		default:
			return fmt.Errorf("unsupported WebSocket opcode %d", opcode)
		}
	}
}

func (c *webSocketClient) writeFrame(opcode byte, payload []byte) error {
	if len(payload) > maxWebSocketPayload {
		return errors.New("WebSocket payload too large")
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	var header [14]byte
	header[0] = 0x80 | opcode
	position := 2
	switch length := len(payload); {
	case length < 126:
		header[1] = 0x80 | byte(length)
	case length <= 65535:
		header[1] = 0x80 | 126
		binary.BigEndian.PutUint16(header[2:4], uint16(length))
		position = 4
	default:
		header[1] = 0x80 | 127
		binary.BigEndian.PutUint64(header[2:10], uint64(length))
		position = 10
	}
	var mask [4]byte
	if _, err := rand.Read(mask[:]); err != nil {
		return err
	}
	copy(header[position:position+4], mask[:])
	position += 4
	masked := make([]byte, len(payload))
	for index := range payload {
		masked[index] = payload[index] ^ mask[index%4]
	}
	if _, err := c.conn.Write(header[:position]); err != nil {
		return err
	}
	_, err := c.conn.Write(masked)
	return err
}

func (c *webSocketClient) readFrame() (byte, []byte, error) {
	first, err := c.reader.ReadByte()
	if err != nil {
		return 0, nil, err
	}
	second, err := c.reader.ReadByte()
	if err != nil {
		return 0, nil, err
	}
	if first&0x80 == 0 {
		return 0, nil, errors.New("fragmented WebSocket frames are not supported")
	}
	if second&0x80 != 0 {
		return 0, nil, errors.New("server sent a masked WebSocket frame")
	}
	length := uint64(second & 0x7f)
	if length == 126 {
		var size [2]byte
		if _, err := io.ReadFull(c.reader, size[:]); err != nil {
			return 0, nil, err
		}
		length = uint64(binary.BigEndian.Uint16(size[:]))
	}
	if length == 127 {
		var size [8]byte
		if _, err := io.ReadFull(c.reader, size[:]); err != nil {
			return 0, nil, err
		}
		length = binary.BigEndian.Uint64(size[:])
	}
	if length > maxWebSocketPayload {
		return 0, nil, errors.New("WebSocket payload too large")
	}
	payload := make([]byte, int(length))
	if _, err := io.ReadFull(c.reader, payload); err != nil {
		return 0, nil, err
	}
	return first & 0x0f, payload, nil
}
