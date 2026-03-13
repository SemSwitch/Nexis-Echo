package model

import "time"

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

// SourceState tracks the processing state of a single event source.
type SourceState struct {
	Source     string    `json:"source"`
	Subject    string    `json:"subject,omitempty"`
	LastSeenAt time.Time `json:"last_seen_at"`
	EventCount int64     `json:"event_count"`
	Active     bool      `json:"active"`
}
