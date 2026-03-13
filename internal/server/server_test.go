package server

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHealthEndpoint(t *testing.T) {
	srv := New(Config{}, Dependencies{
		Health: staticHealthProvider{
			report: HealthReport{
				Status:    "degraded",
				Timestamp: time.Date(2026, time.March, 12, 22, 0, 0, 0, time.UTC),
				Components: map[string]string{
					"nats": "starting",
				},
			},
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rec.Code)
	}

	var report HealthReport
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if report.Status != "degraded" {
		t.Fatalf("expected status degraded, got %q", report.Status)
	}

	if got := report.Components["nats"]; got != "starting" {
		t.Fatalf("expected nats component to be starting, got %q", got)
	}
}

func TestWebSocketEndpointHandshakeAndInitialEvent(t *testing.T) {
	srv := New(Config{}, Dependencies{})
	testServer := httptest.NewServer(srv.Handler())
	defer testServer.Close()

	addr := strings.TrimPrefix(testServer.URL, "http://")
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial server: %v", err)
	}
	defer conn.Close()

	request := strings.Join([]string{
		"GET /ws HTTP/1.1",
		"Host: " + addr,
		"Upgrade: websocket",
		"Connection: Upgrade",
		"Sec-WebSocket-Version: 13",
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==",
		"",
		"",
	}, "\r\n")

	if _, err := conn.Write([]byte(request)); err != nil {
		t.Fatalf("write request: %v", err)
	}

	reader := bufio.NewReader(conn)

	statusLine, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read status line: %v", err)
	}
	if !strings.Contains(statusLine, "101 Switching Protocols") {
		t.Fatalf("expected 101 switching protocols, got %q", strings.TrimSpace(statusLine))
	}

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read header: %v", err)
		}
		if line == "\r\n" {
			break
		}
	}

	frame, err := readFrame(reader)
	if err != nil {
		t.Fatalf("read websocket frame: %v", err)
	}

	var event LiveEvent
	if err := json.Unmarshal(frame, &event); err != nil {
		t.Fatalf("unmarshal websocket frame: %v", err)
	}

	if event.Type != "connected" {
		t.Fatalf("expected connected event, got %q", event.Type)
	}
}

type staticHealthProvider struct {
	report HealthReport
}

func (p staticHealthProvider) Health(context.Context) HealthReport {
	return p.report
}

func readFrame(reader *bufio.Reader) ([]byte, error) {
	header, err := reader.ReadByte()
	if err != nil {
		return nil, err
	}
	if header&0x0f != websocketOpcodeText {
		return nil, fmt.Errorf("unexpected opcode: %d", header&0x0f)
	}

	lengthByte, err := reader.ReadByte()
	if err != nil {
		return nil, err
	}

	payloadLength := int(lengthByte & 0x7f)
	payload := make([]byte, payloadLength)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return nil, err
	}

	return payload, nil
}
