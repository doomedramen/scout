package control

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

type Database interface{ PingContext(context.Context) error }

// Handler exposes only development health information. Owner authentication must
// precede any inventory, telemetry, credential, or enrollment endpoints.
func Handler(db Database) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		state := "not configured"
		if db != nil {
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			state = "connected"
			if err := db.PingContext(ctx); err != nil {
				state = "unavailable"
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_ = json.NewEncoder(w).Encode(struct {
			Mode       string `json:"mode"`
			Database   string `json:"database"`
			Enrollment bool   `json:"enrollmentAvailable"`
		}{"development", state, false})
	})
	return mux
}
