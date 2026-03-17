package handlers

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/peiblow/eeapi/internal/service"
)

func TraceHandler(svc service.ContractService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		contextID := chi.URLParam(r, "contextId")

		trace, err := svc.TraceContext(r.Context(), contextID)
		if err != nil {
			slog.Error("Failed to trace context", "error", err, "context_id", contextID) // ← log
			http.Error(w, "Context not found", http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(trace)
	}
}
