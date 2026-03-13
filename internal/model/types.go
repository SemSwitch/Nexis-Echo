package model

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Severity classifies the urgency or outcome of a notification.
type Severity int

const (
	SeveritySuccess Severity = iota
	SeverityInfo
	SeverityWarning
	SeverityError
)

func (s Severity) String() string {
	switch s {
	case SeveritySuccess:
		return "success"
	case SeverityInfo:
		return "info"
	case SeverityWarning:
		return "warning"
	case SeverityError:
		return "error"
	default:
		return "unknown"
	}
}

// MarshalJSON keeps severity payloads stable and readable in runtime events.
func (s Severity) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.String())
}

// UnmarshalJSON accepts either a string label or legacy integer severity.
func (s *Severity) UnmarshalJSON(data []byte) error {
	if len(data) == 0 {
		return nil
	}

	if data[0] == '"' {
		var raw string
		if err := json.Unmarshal(data, &raw); err != nil {
			return err
		}

		parsed, err := parseSeverity(raw)
		if err != nil {
			return err
		}

		*s = parsed
		return nil
	}

	var raw int
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("severity must be a string or integer: %w", err)
	}

	*s = Severity(raw)
	return nil
}

func parseSeverity(value string) (Severity, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "success":
		return SeveritySuccess, nil
	case "info":
		return SeverityInfo, nil
	case "warning", "warn":
		return SeverityWarning, nil
	case "error":
		return SeverityError, nil
	default:
		return SeverityInfo, fmt.Errorf("unknown severity %q", value)
	}
}

// RawOutput represents an unprocessed event received from a source stream.
type RawOutput struct {
	Source  string    `json:"source"`
	Subject string    `json:"subject,omitempty"`
	Chunk   string    `json:"chunk"`
	TS      time.Time `json:"ts"`
}

// OutputClosed represents a finalized output block ready for summarization.
type OutputClosed struct {
	Source     string    `json:"source"`
	Subject    string    `json:"subject,omitempty"`
	Status     string    `json:"status,omitempty"`
	DurationMS int64     `json:"duration_ms,omitempty"`
	Final      bool      `json:"final"`
	TS         time.Time `json:"ts"`
}

// NotificationReady is a summarized output ready to enter the notification pipeline.
type NotificationReady struct {
	EventID  string    `json:"event_id,omitempty"`
	Source   string    `json:"source"`
	Summary  string    `json:"summary"`
	Excerpt  string    `json:"excerpt,omitempty"`
	Severity Severity  `json:"severity"`
	TS       time.Time `json:"ts"`
}

// NotificationRecord is a persisted record of a delivered notification.
type NotificationRecord struct {
	EventID     string    `json:"event_id"`
	Source      string    `json:"source"`
	Summary     string    `json:"summary"`
	Excerpt     string    `json:"excerpt,omitempty"`
	Severity    Severity  `json:"severity"`
	DeliveredAt time.Time `json:"delivered_at"`
	Channel     string    `json:"channel"`
	TTSQueued   bool      `json:"tts_queued"`
}

// OutputBlock captures a finalized terminal-output block retained in memory
// for later summarization or inspection.
type OutputBlock struct {
	ID         string    `json:"id"`
	Source     string    `json:"source"`
	Subject    string    `json:"subject,omitempty"`
	Content    string    `json:"content"`
	Status     string    `json:"status,omitempty"`
	DurationMS int64     `json:"duration_ms,omitempty"`
	ClosedAt   time.Time `json:"closed_at"`
	Final      bool      `json:"final"`
}

// SourceState tracks the processing state of a single event source.
type SourceState struct {
	Source        string    `json:"source"`
	Subject       string    `json:"subject,omitempty"`
	LastSeenAt    time.Time `json:"last_seen_at"`
	LastClosedAt  time.Time `json:"last_closed_at,omitempty"`
	LastStatus    string    `json:"last_status,omitempty"`
	EventCount    int64     `json:"event_count"`
	BufferedBytes int       `json:"buffered_bytes"`
	ClosedCount   int64     `json:"closed_count"`
	Active        bool      `json:"active"`
}

// RuntimeEvent is the runtime-owned live event shape used to bridge in-memory
// runtime activity into transports such as WebSockets.
type RuntimeEvent struct {
	Type   string    `json:"type"`
	Source string    `json:"source,omitempty"`
	TS     time.Time `json:"ts"`
	Data   any       `json:"data,omitempty"`
}
