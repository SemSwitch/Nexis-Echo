package server

import (
	"bufio"
	"context"
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

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgradeWebSocket(w, r)
	if err != nil {
		s.logger.Debug("websocket upgrade failed", "error", err)
		return
	}
	defer conn.Close()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	go func() {
		defer cancel()
		if err := conn.readLoop(); err != nil && !errors.Is(err, net.ErrClosed) {
			s.logger.Debug("websocket read loop stopped", "error", err)
		}
	}()

	if err := conn.writeJSON(s.cfg.WriteTimeout, LiveEvent{
		Type:      "connected",
		Timestamp: time.Now().UTC(),
		Data: map[string]string{
			"status": "ready",
		},
	}); err != nil {
		return
	}

	var (
		liveCh <-chan LiveEvent
	)

	if s.live != nil {
		liveCh, err = s.live.Subscribe(ctx)
		if err != nil {
			s.logger.Debug("live subscription failed", "error", err)
			_ = conn.writeClose(s.cfg.WriteTimeout, websocketStatusInternalError, "subscription failed")
			return
		}
	}

	pingTicker := time.NewTicker(s.cfg.PingInterval)
	defer pingTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			_ = conn.writeClose(s.cfg.WriteTimeout, websocketStatusNormalClosure, "server closing")
			return
		case <-pingTicker.C:
			if err := conn.writePing(s.cfg.WriteTimeout); err != nil {
				return
			}
		case event, ok := <-liveCh:
			if !ok {
				liveCh = nil
				continue
			}
			if event.Timestamp.IsZero() {
				event.Timestamp = time.Now().UTC()
			}
			if err := conn.writeJSON(s.cfg.WriteTimeout, event); err != nil {
				return
			}
		}
	}
}

var errHandshake = errors.New("invalid websocket handshake")

func upgradeWebSocket(w http.ResponseWriter, r *http.Request) (*websocketConn, error) {
	if r.Method != http.MethodGet {
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return nil, fmt.Errorf("%w: method %s", errHandshake, r.Method)
	}
	if !headerContainsToken(r.Header, "Connection", "upgrade") {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return nil, fmt.Errorf("%w: missing upgrade token", errHandshake)
	}
	if !headerEqualsFold(r.Header, "Upgrade", "websocket") {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return nil, fmt.Errorf("%w: invalid upgrade header", errHandshake)
	}

	key := strings.TrimSpace(r.Header.Get("Sec-WebSocket-Key"))
	if key == "" {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return nil, fmt.Errorf("%w: missing websocket key", errHandshake)
	}

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return nil, errors.New("response writer does not support hijacking")
	}

	netConn, rw, err := hijacker.Hijack()
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return nil, err
	}

	accept := websocketAccept(key)
	if _, err := rw.WriteString(
		"HTTP/1.1 101 Switching Protocols\r\n" +
			"Upgrade: websocket\r\n" +
			"Connection: Upgrade\r\n" +
			"Sec-WebSocket-Accept: " + accept + "\r\n" +
			"\r\n",
	); err != nil {
		_ = netConn.Close()
		return nil, err
	}
	if err := rw.Flush(); err != nil {
		_ = netConn.Close()
		return nil, err
	}

	return &websocketConn{
		conn: netConn,
		rw:   rw,
	}, nil
}

func websocketAccept(key string) string {
	sum := sha1.Sum([]byte(key + websocketGUID))
	return base64.StdEncoding.EncodeToString(sum[:])
}

func headerContainsToken(header http.Header, key string, token string) bool {
	for _, value := range header.Values(key) {
		for _, part := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(part), token) {
				return true
			}
		}
	}

	return false
}

func headerEqualsFold(header http.Header, key string, value string) bool {
	return strings.EqualFold(strings.TrimSpace(header.Get(key)), value)
}

type websocketConn struct {
	conn net.Conn
	rw   *bufio.ReadWriter
	mu   sync.Mutex
}

func (c *websocketConn) Close() error {
	return c.conn.Close()
}

func (c *websocketConn) writeJSON(timeout time.Duration, value interface{}) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}

	return c.writeFrame(timeout, websocketOpcodeText, payload)
}

func (c *websocketConn) writePing(timeout time.Duration) error {
	return c.writeFrame(timeout, websocketOpcodePing, nil)
}

func (c *websocketConn) writeClose(timeout time.Duration, code uint16, reason string) error {
	payload := make([]byte, 2)
	binary.BigEndian.PutUint16(payload, code)
	payload = append(payload, []byte(reason)...)
	return c.writeFrame(timeout, websocketOpcodeClose, payload)
}

func (c *websocketConn) writeFrame(timeout time.Duration, opcode byte, payload []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if timeout > 0 {
		if err := c.conn.SetWriteDeadline(time.Now().Add(timeout)); err != nil {
			return err
		}
		defer c.conn.SetWriteDeadline(time.Time{})
	}

	header := []byte{0x80 | opcode}

	switch {
	case len(payload) < 126:
		header = append(header, byte(len(payload)))
	case len(payload) <= 65535:
		header = append(header, 126, byte(len(payload)>>8), byte(len(payload)))
	default:
		header = append(header, 127)
		length := make([]byte, 8)
		binary.BigEndian.PutUint64(length, uint64(len(payload)))
		header = append(header, length...)
	}

	if _, err := c.rw.Write(header); err != nil {
		return err
	}
	if len(payload) > 0 {
		if _, err := c.rw.Write(payload); err != nil {
			return err
		}
	}

	return c.rw.Flush()
}

func (c *websocketConn) readLoop() error {
	for {
		header := make([]byte, 2)
		if _, err := io.ReadFull(c.rw, header); err != nil {
			return err
		}

		opcode := header[0] & 0x0f
		masked := header[1]&0x80 != 0
		payloadLength := uint64(header[1] & 0x7f)

		switch payloadLength {
		case 126:
			extended := make([]byte, 2)
			if _, err := io.ReadFull(c.rw, extended); err != nil {
				return err
			}
			payloadLength = uint64(binary.BigEndian.Uint16(extended))
		case 127:
			extended := make([]byte, 8)
			if _, err := io.ReadFull(c.rw, extended); err != nil {
				return err
			}
			payloadLength = binary.BigEndian.Uint64(extended)
		}

		var maskKey [4]byte
		if masked {
			if _, err := io.ReadFull(c.rw, maskKey[:]); err != nil {
				return err
			}
		}

		if payloadLength > 1<<20 {
			return fmt.Errorf("websocket frame too large: %d", payloadLength)
		}

		payload := make([]byte, int(payloadLength))
		if _, err := io.ReadFull(c.rw, payload); err != nil {
			return err
		}

		if masked {
			for i := range payload {
				payload[i] ^= maskKey[i%4]
			}
		}

		switch opcode {
		case websocketOpcodeClose:
			return nil
		case websocketOpcodePing:
			if err := c.writeFrame(0, websocketOpcodePong, payload); err != nil {
				return err
			}
		case websocketOpcodePong:
			continue
		default:
			continue
		}
	}
}

const (
	websocketOpcodeText  = 0x1
	websocketOpcodeClose = 0x8
	websocketOpcodePing  = 0x9
	websocketOpcodePong  = 0xA
)

const (
	websocketStatusNormalClosure = 1000
	websocketStatusInternalError = 1011
)
