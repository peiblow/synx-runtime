package service

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
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

	slog.Info("Executing contract", "contract_id", contractID, "function", payload.Function)
	timestamp := time.Now().UTC().UnixMilli()

	contract, err := s.db.GetContractByID(ctx, contractID)
	if err != nil {
		slog.Error("Failed to retrieve contract", "contract_id", contractID, "error", err)
		return &ExecuteResult{
			BlockHash: "",
			Response: &swp.WireResponse{
				Type:    swp.EXEC,
				ID:      uuid.New().String(),
				Success: false,
				Error:   "Failed to retrieve contract: " + err.Error(),
			},
		}, err
	}

	slog.Info("Retrieving contract artifact", "artifact_hash", contract.ArtifactHash)
	artifact, err := s.db.GetContractArtifactByHash(ctx, contract.ArtifactHash)
	if err != nil {
		slog.Error("Failed to retrieve contract artifact", "artifact_hash", contract.ArtifactHash, "error", err)
		return &ExecuteResult{
			BlockHash: "",
			Response: &swp.WireResponse{
				Type:    swp.EXEC,
				ID:      uuid.New().String(),
				Success: false,
				Error:   "Failed to retrieve contract artifact: " + err.Error(),
			},
		}, err
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

	var resp swp.WireResponse
	if err := s.swpClient.Send(msg, &resp); err != nil {
		return &ExecuteResult{
			BlockHash: "",
			Response: &swp.WireResponse{
				Type:    swp.EXEC,
				ID:      msg.ID,
				Success: false,
				Error:   "Failed to execute contract: " + err.Error(),
			},
		}, err
	}

	if resp.Success == false {
		return &ExecuteResult{
			BlockHash: "",
			Response: &swp.WireResponse{
				Type:    swp.EXEC,
				ID:      msg.ID,
				Success: false,
				Error:   "Contract execution failed: " + string(resp.Error),
			},
		}, fmt.Errorf("contract execution failed: %s", string(resp.Error))
	}

	var respData swp.ExecResponse
	if err := json.Unmarshal(resp.Data, &respData); err != nil {
		return nil, err
	}

	previousBlock, err := s.blockDB.GetLastContractBlock(ctx, contractID)
	if err != nil {
		slog.Error("Failed to retrieve last block", "error", err)
		return nil, err
	}

	journalBytes, err := json.Marshal(respData.Journal)
	if err != nil {
		slog.Error("Failed to marshal journal", "error", err)
		return nil, err
	}

	journalHashRaw := sha256.Sum256(append(journalBytes, []byte(fmt.Sprintf("%d", timestamp))...))
	journalHash := "0x" + hex.EncodeToString(journalHashRaw[:])

	blockData := fmt.Sprintf(
		"%d|%s|%s|%s|%s|%s",
		timestamp,
		previousBlock.Hash,
		journalHash,
		contractID,
		payload.Function,
		respData.ArtifactHash,
	)
	blocHashRaw := sha256.Sum256([]byte(blockData))
	blockHash := "0x" + hex.EncodeToString(blocHashRaw[:])

	encryptedJournal, err := keys.EncryptJournal(journalBytes, s.privKey)
	if err != nil {
		slog.Error("Failed to encrypt journal", "error", err)
		return nil, err
	}

	signature := ed25519.Sign(s.privKey, blocHashRaw[:])

	slog.Info("Saving execution block", "block_hash", blockHash, "previous_hash", previousBlock.Hash, "journal_hash", journalHash, "contract_id", contractID, "function", payload.Function)
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
	}

	if err := blocks.VerifyBlock(*previousBlock, *block, journalBytes, s.pubKey); err != nil {
		return nil, err
	}
	slog.Info("Block verification successful", "block_hash", block.Hash)

	if err := s.blockDB.SaveBlock(ctx, block); err != nil {
		slog.Error("Failed to save execution block", "error", err)
		return nil, err
	}
	slog.Info("Execution block saved successfully", "block_hash", block.Hash)

	slog.Info("Contract executed successfully", "contract_hash", respData.ArtifactHash, "function", respData.Function, "exec_price", respData.ExecPrice)
	return &ExecuteResult{
		BlockHash: blockHash,
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
