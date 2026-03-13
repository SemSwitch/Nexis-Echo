package model

import "time"

// ServiceState represents the lifecycle state of a runtime service.
type ServiceState int

const (
	StateIdle ServiceState = iota
	StateStarting
	StateRunning
	StateDegraded
	StateStopping
	StateStopped
)

func (s ServiceState) String() string {
	switch s {
	case StateIdle:
		return "idle"
	case StateStarting:
		return "starting"
	case StateRunning:
		return "running"
	case StateDegraded:
		return "degraded"
	case StateStopping:
		return "stopping"
	case StateStopped:
		return "stopped"
	default:
		return "unknown"
	}
}

// ServiceHealth reports the health of a single runtime service.
type ServiceHealth struct {
	Name      string       `json:"name"`
	State     ServiceState `json:"state"`
	Message   string       `json:"message,omitempty"`
	CheckedAt time.Time    `json:"checked_at"`
}
