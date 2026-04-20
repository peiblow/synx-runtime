package service

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/peiblow/eeapi/internal/blocks"
	"github.com/peiblow/eeapi/internal/config"
	"github.com/peiblow/eeapi/internal/database/postgres"
	"github.com/peiblow/eeapi/internal/keys"
	"github.com/peiblow/eeapi/internal/repository"
	"github.com/peiblow/eeapi/internal/schema"
	"github.com/peiblow/eeapi/internal/swp"
)

type ContractService interface {
	DeployContract(ctx context.Context, payload *swp.DeployPayload) (*swp.WireResponse, error)
	ExecuteContract(ctx context.Context, contractID string, payload *swp.ExecPayload) (*ExecuteResult, error)
	TraceContext(ctx context.Context, contextID string) (*TraceOutput, error)
}

type contractService struct {
	swpClient *swp.SwpClient
	db        repository.ContractRepository
	blockDB   repository.BlockRepository
	privKey   []byte
	pubKey    []byte
	locker    *config.ContractLocker
}

func NewContractService(swpClient *swp.SwpClient, db *postgres.DB, privKey []byte, pubKey []byte, locker *config.ContractLocker) ContractService {
	return &contractService{
		swpClient: swpClient,
		db:        repository.NewPsqlContractRepository(db),
		blockDB:   repository.NewPsqlBlockRepository(db),
		privKey:   privKey,
		pubKey:    pubKey,
		locker:    locker,
	}
}

type ArtifactMetadata struct {
	ConstPool    []interface{}          `json:"const_pool"`
	Functions    map[string]interface{} `json:"functions"`
	FunctionName map[int]string         `json:"function_name"`
	Types        map[string]interface{} `json:"types"`
	InitStorage  map[string]interface{} `json:"init_storage"`
}

type ExecuteResult struct {
	BlockHash string
	Events    []interface{}
	Response  *swp.WireResponse
}

type TraceStep struct {
	Function      string `json:"function"`
	ExecutionHash string `json:"executionHash"`
	ParentHash    string `json:"parentHash"`
	ContractID    string `json:"contract"`
	Timestamp     int64  `json:"executedAt"`
}

type TraceOutput struct {
	ContextID string      `json:"contextId"`
	Status    string      `json:"status"`
	Steps     []TraceStep `json:"steps"`
}

type EnrichedJournal struct {
	Events []interface{}          `json:"events"`
	Args   map[string]interface{} `json:"args"`
	Trace  []swp.TraceLog         `json:"trace"`
	Audit  []swp.AuditLog         `json:"audit"`
}

func (s *contractService) DeployContract(ctx context.Context, payload *swp.DeployPayload) (*swp.WireResponse, error) {

	createdAt := time.Now().UTC()
	hashInput := fmt.Sprintf("%v:%v:%v:%v", payload.Owner, payload.ContractName, payload.Version, createdAt.UnixMilli())
	hashBytes := sha256.Sum256([]byte(hashInput))
	hash := "0x" + hex.EncodeToString(hashBytes[:])

	msg := swp.WireMesage{
		Type: swp.DEPLOY,
		ID:   uuid.New().String(),
		Data: swp.DeployPayload{
			Hash:         hash,
			ContractName: payload.ContractName,
			Version:      payload.Version,
			Owner:        payload.Owner,
			Source:       payload.Source,
		},
	}

	var resp swp.WireResponse
	if err := s.swpClient.Send(msg, &resp); err != nil {
		return nil, err
	}

	if resp.Success == false {
		return &resp, fmt.Errorf("contract deployment failed: %s", string(resp.Error))
	}

	var respData swp.DeployResponse
	if err := json.Unmarshal(resp.Data, &respData); err != nil {
		return nil, err
	}

	if err := s.db.SaveAgentMeta(ctx, &swp.AgentMeta{
		Hash:    respData.Agent.Hash,
		Name:    respData.Agent.Name,
		Version: respData.Agent.Version,
	}); err != nil {
		return nil, err
	}
	slog.Info("Agent meta saved successfully", "agent_hash", respData.Agent.Hash)

	if err := s.db.SaveContractArtifact(ctx, hash, respData.Agent.Hash, &respData.ContractArtifact); err != nil {
		return nil, err
	}
	slog.Info("Contract artifact saved successfully", "contract_hash", respData.ContractHash)

	if err := s.db.SaveContract(ctx, &schema.Contract{
		Name:         respData.ContractName,
		Owner:        respData.ContractOwner,
		ArtifactHash: hash,
		CreatedAt:    createdAt.UnixMilli(),
	}); err != nil {
		return nil, err
	}
	slog.Info("Contract deployed successfully", "contract_hash", respData.ContractHash)

	return &resp, nil
}

func (s *contractService) ExecuteContract(ctx context.Context, contractID string, payload *swp.ExecPayload) (*ExecuteResult, error) {
	s.locker.Lock(contractID)
	defer s.locker.Unlock(contractID)

	executionStart := time.Now().UTC()
	auditLogs := []swp.AuditLog{{
		Time:  executionStart.Format("15:04:05.000"),
		Event: "Execution requested",
		Actor: payload.CallerID,
	}}
	traceLogs := []swp.TraceLog{}

	slog.Info("Executing contract", "contract_id", contractID, "function", payload.Function)
	timestamp := time.Now().UTC().UnixMilli()

	// ── contract.load ─────────────────────────────────────────────────────────
	stepStart := time.Now()
	contract, err := s.db.GetContractByID(ctx, contractID)
	if err != nil {
		return &ExecuteResult{
			BlockHash: "",
			Response:  &swp.WireResponse{Type: swp.EXEC, ID: uuid.New().String(), Success: false, Error: "Failed to retrieve contract: " + err.Error()},
		}, err
	}
	traceLogs = append(traceLogs, swp.TraceLog{Step: "contract.load", Msg: fmt.Sprintf("Contract loaded: %s", contractID), DurationMs: time.Since(stepStart).Milliseconds()})
	auditLogs = append(auditLogs, swp.AuditLog{Time: time.Now().UTC().Format("15:04:05.000"), Event: fmt.Sprintf("Contract loaded: %s", contract.ArtifactHash), Actor: "system"})

	// ── artifact.load ─────────────────────────────────────────────────────────
	stepStart = time.Now()
	artifact, err := s.db.GetContractArtifactByHash(ctx, contract.ArtifactHash)
	if err != nil {
		slog.Error("Failed to retrieve contract artifact", "artifact_hash", contract.ArtifactHash, "error", err)
		return &ExecuteResult{
			BlockHash: "",
			Response:  &swp.WireResponse{Type: swp.EXEC, ID: uuid.New().String(), Success: false, Error: "Failed to retrieve contract artifact: " + err.Error()},
		}, err
	}
	traceLogs = append(traceLogs, swp.TraceLog{Step: "artifact.load", Msg: fmt.Sprintf("Artifact loaded: %s", contract.ArtifactHash), DurationMs: time.Since(stepStart).Milliseconds()})

	// ── VVM execute ───────────────────────────────────────────────────────────
	if payload.ContextId != "" {
		finalBlock, err := s.blockDB.GetFinalBlockByContextID(ctx, payload.ContextId)
		if err == nil && finalBlock != nil {
			reason := ""
			if finalBlock.FunctionName == "pow" {
				reason = "context already finalized with 'pow'"
			} else if finalBlock.Status == "rejected" {
				reason = "context already rejected — start a new context"
			}
			if reason != "" {
				return &ExecuteResult{
					BlockHash: "",
					Response:  &swp.WireResponse{Type: swp.EXEC, ID: uuid.New().String(), Success: false, Error: reason},
				}, fmt.Errorf(reason)
			}
		}
	}

	msg := swp.WireMesage{
		Type: swp.EXEC,
		ID:   uuid.New().String(),
		Data: swp.ExecPayload{
			ContractArtifact: *artifact,
			ArtifactHash:     contract.ArtifactHash,
			Function:         payload.Function,
			Args:             payload.Args,
		},
	}

	stepStart = time.Now()
	var resp swp.WireResponse
	if err := s.swpClient.Send(msg, &resp); err != nil {
		return &ExecuteResult{
			BlockHash: "",
			Response:  &swp.WireResponse{Type: swp.EXEC, ID: msg.ID, Success: false, Error: "Failed to execute contract: " + err.Error()},
		}, err
	}
	execDuration := time.Since(stepStart).Milliseconds()

	previousBlock, err := s.blockDB.GetLastContractBlock(ctx, contractID)
	if err != nil {
		slog.Error("Failed to retrieve last block", "error", err)
		return nil, err
	}

	// ── determina status ──────────────────────────────────────────────────────
	blockStatus := "approved"
	failedReason := ""

	if !resp.Success {
		blockStatus = "rejected"
		failedReason = extractFailedReason(string(resp.Error))
		auditLogs = append(auditLogs, swp.AuditLog{Time: time.Now().UTC().Format("15:04:05.000"), Event: fmt.Sprintf("Function failed: %s — %s", payload.Function, failedReason), Actor: "vvm"})
		traceLogs = append(traceLogs, swp.TraceLog{Step: payload.Function, Msg: fmt.Sprintf("Function '%s' failed: %s", payload.Function, failedReason), DurationMs: execDuration})
	} else {
		auditLogs = append(auditLogs, swp.AuditLog{Time: time.Now().UTC().Format("15:04:05.000"), Event: fmt.Sprintf("Function executed: %s", payload.Function), Actor: "vvm"})
		traceLogs = append(traceLogs, swp.TraceLog{Step: payload.Function, Msg: fmt.Sprintf("Function '%s' executed successfully", payload.Function), DurationMs: execDuration})
	}

	// ── journal ───────────────────────────────────────────────────────────────
	var journalEvents []interface{}
	var artifactHash string
	if resp.Success {
		var respData swp.ExecResponse
		if err := json.Unmarshal(resp.Data, &respData); err != nil {
			return nil, err
		}
		journalEvents = respData.Journal
		artifactHash = respData.ArtifactHash
	}

	enrichedJournal := EnrichedJournal{
		Events: journalEvents,
		Args:   payload.Args,
		Trace:  traceLogs,
		Audit:  auditLogs,
	}

	journalBytes, err := json.Marshal(enrichedJournal)
	if err != nil {
		slog.Error("Failed to marshal journal", "error", err)
		return nil, err
	}

	// ── hashes + assinatura ───────────────────────────────────────────────────
	journalHashRaw := sha256.Sum256(append(journalBytes, []byte(fmt.Sprintf("%d", timestamp))...))
	journalHash := "0x" + hex.EncodeToString(journalHashRaw[:])

	blockData := fmt.Sprintf("%d|%s|%s|%s|%s|%s",
		timestamp, previousBlock.Hash, journalHash, contractID, payload.Function, artifactHash,
	)
	blockHashRaw := sha256.Sum256([]byte(blockData))
	blockHash := "0x" + hex.EncodeToString(blockHashRaw[:])

	encryptedJournal, err := keys.EncryptJournal(journalBytes, s.privKey)
	if err != nil {
		slog.Error("Failed to encrypt journal", "error", err)
		return nil, err
	}

	signature := ed25519.Sign(s.privKey, blockHashRaw[:])

	// ── salva block (sempre — sucesso ou falha do VVM) ────────────────────────
	block := &schema.Block{
		BlockIndex:   previousBlock.BlockIndex + 1,
		Hash:         blockHash,
		Timestamp:    timestamp,
		PreviousHash: previousBlock.Hash,
		JournalHash:  journalHash,
		Signature:    signature,
		ContractID:   contractID,
		FunctionName: payload.Function,
		Journal:      encryptedJournal,
		ContextID:    payload.ContextId,
		Status:       blockStatus,
		FailedReason: failedReason,
	}

	if resp.Success {
		if err := blocks.VerifyBlock(*previousBlock, *block, journalBytes, s.pubKey); err != nil {
			return nil, err
		}
		slog.Info("Block verification successful", "block_hash", block.Hash)
	}

	slog.Info("Saving execution block", "block_hash", blockHash, "status", blockStatus, "function", payload.Function)

	if err := s.blockDB.SaveBlock(ctx, block); err != nil {
		slog.Error("Failed to save execution block", "error", err)
		return nil, err
	}
	slog.Info("Execution block saved successfully", "block_hash", block.Hash)

	// ── retorno ───────────────────────────────────────────────────────────────
	if !resp.Success {
		return &ExecuteResult{
			BlockHash: blockHash,
			Events:    nil,
			Response:  &swp.WireResponse{Type: swp.EXEC, ID: msg.ID, Success: false, Error: resp.Error},
		}, fmt.Errorf("contract execution failed: %s", string(resp.Error))
	}

	var respData swp.ExecResponse
	if err := json.Unmarshal(resp.Data, &respData); err != nil {
		return nil, err
	}

	slog.Info("Contract executed successfully", "contract_hash", respData.ArtifactHash, "function", respData.Function, "status", blockStatus)

	return &ExecuteResult{
		BlockHash: blockHash,
		Events:    respData.Journal,
		Response:  &resp,
	}, nil
}

func (s *contractService) TraceContext(ctx context.Context, contextID string) (*TraceOutput, error) {
	blocks, err := s.blockDB.GetBlocksByContextID(ctx, contextID)
	if err != nil {
		return nil, err
	}

	steps := make([]TraceStep, len(blocks))
	for i, block := range blocks {
		steps[i] = TraceStep{
			Function:      block.FunctionName,
			ExecutionHash: block.Hash,
			ParentHash:    block.PreviousHash,
			ContractID:    block.ContractID,
			Timestamp:     block.Timestamp,
		}
	}

	return &TraceOutput{
		ContextID: contextID,
		Status:    "COMPLETED",
		Steps:     steps,
	}, nil
}

func extractFailedReason(rawError string) string {
	s := rawError
	if idx := strings.Index(s, "[execution error: "); idx != -1 {
		s = s[idx+len("[execution error: "):]
		s = strings.TrimSuffix(strings.TrimSpace(s), "]")
	}
	s = strings.TrimPrefix(s, "map[Value:")
	s = strings.TrimSuffix(s, "]")

	return strings.TrimSpace(s)
}
