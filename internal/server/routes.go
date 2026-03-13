package server

import (
	"encoding/json"
	"net/http"
	"time"
)

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("GET /api/health", s.handleHealth)
	s.mux.HandleFunc("GET /api/sources", s.handleSources)
	s.mux.HandleFunc("GET "+s.cfg.WebSocketPath, s.handleWebSocket)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	report := defaultHealthReport()
	if s.health != nil {
		report = s.health.Health(r.Context())
	}

	if report.Timestamp.IsZero() {
		report.Timestamp = time.Now().UTC()
	}

	if report.Status == "" {
		report.Status = "ok"
	}

	writeJSON(w, http.StatusOK, report)
}

func defaultHealthReport() HealthReport {
	return HealthReport{
		Status:    "ok",
		Timestamp: time.Now().UTC(),
		Components: map[string]string{
			"server": "ok",
		},
	}
}

func (s *Server) handleSources(w http.ResponseWriter, r *http.Request) {
	snapshots := []SourceSnapshot{}
	if s.sources != nil {
		snapshots = s.sources.Sources(r.Context())
		if snapshots == nil {
			snapshots = []SourceSnapshot{}
		}
	}

	writeJSON(w, http.StatusOK, snapshots)
}

func writeJSON(w http.ResponseWriter, status int, value interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value)
}
