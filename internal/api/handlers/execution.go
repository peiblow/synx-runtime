package handlers

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/peiblow/eeapi/internal/service"
	"github.com/peiblow/eeapi/internal/swp"
)

type ExecApiResponse struct {
	id       string                   `json:"id"`
	Price    int                      `json:"price"`
	Function string                   `json:"function"`
	Journal  []map[string]interface{} `json:"journal"`
}

func ExecHandler(svc service.ContractService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")

		var req swp.ExecPayload
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			slog.Error("Invalid request payload", "error", err)
			http.Error(w, "Invalid request payload", http.StatusBadRequest)
			return
		}

		slog.Info("Executing contract", "id", id, "function", req.Function, "args", req.Args, "contextId", req.ContextId)

		result, err := svc.ExecuteContract(r.Context(), id, &req)
		if err != nil {
			http.Error(w, "Failed to execute contract: "+err.Error(), http.StatusInternalServerError)
			slog.Error("Failed to execute contract", "error", err)
			return
		}

		var resp ExecApiResponse
		if err := json.Unmarshal(result.Data, &resp); err != nil {
			http.Error(w, "Failed to parse response: "+err.Error(), http.StatusInternalServerError)
			slog.Error("Failed to parse response", "error", err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}
}
