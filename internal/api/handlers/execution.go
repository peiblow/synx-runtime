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
	ExecutionHash string        `json:"executionHash"`
	Function      string        `json:"function"`
	Success       bool          `json:"success"`
	ContextId     string        `json:"contextId"`
	Events        []interface{} `json:"events"`
	Reason        string        `json:"reason,omitempty"`
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

		if !result.Response.Success {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(ExecApiResponse{
				ExecutionHash: result.BlockHash,
				Function:      req.Function,
				Success:       false,
				ContextId:     req.ContextId,
				Events:        nil,
				Reason:        result.FailedReason,
			})
			return
		}

		var coreResp swp.ExecResponse
		if err := json.Unmarshal(result.Response.Data, &coreResp); err != nil {
			http.Error(w, "Failed to parse response: "+err.Error(), http.StatusInternalServerError)
			slog.Error("Failed to parse response", "error", err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ExecApiResponse{
			ExecutionHash: result.BlockHash,
			Function:      coreResp.Function,
			Success:       result.Response.Success,
			ContextId:     req.ContextId,
			Events:        result.Events,
		})
	}
}
