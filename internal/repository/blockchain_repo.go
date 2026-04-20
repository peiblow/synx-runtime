package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/peiblow/eeapi/internal/database/postgres"
	"github.com/peiblow/eeapi/internal/schema"
)

type BlockRepository interface {
	SaveBlock(ctx context.Context, block *schema.Block) error
	GetBlockByID(ctx context.Context, id string) (*schema.Block, error)
	GetLastContractBlock(ctx context.Context, contractId string) (*schema.Block, error)
	GetFinalBlockByContextID(ctx context.Context, contextID string) (*schema.Block, error)
	GetBlocksByContextID(ctx context.Context, contextID string) ([]*schema.Block, error)
}

type PsqlBlockRepository struct {
	db *postgres.DB
}

func NewPsqlBlockRepository(db *postgres.DB) BlockRepository {
	return &PsqlBlockRepository{db: db}
}

func (r *PsqlBlockRepository) SaveBlock(ctx context.Context, block *schema.Block) error {
	query := `
		INSERT INTO blocks (
			block_index, hash, timestamp, previous_hash, journal_hash,
			signature, contract_id, function_name, journal, context_id,
			status, failed_reason
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
	`
	_, err := r.db.ExecContext(ctx, query,
		block.BlockIndex,
		block.Hash,
		block.Timestamp,
		block.PreviousHash,
		block.JournalHash,
		block.Signature,
		block.ContractID,
		block.FunctionName,
		block.Journal,
		block.ContextID,
		block.Status,
		block.FailedReason,
	)
	return err
}

func (r *PsqlBlockRepository) GetBlockByID(ctx context.Context, id string) (*schema.Block, error) {
	query := `SELECT hash, timestamp, previous_hash, journal_hash, signature, contract_id, function_name, journal FROM blocks WHERE id = $1`

	row := r.db.QueryRowContext(ctx, query, id)

	var block schema.Block
	err := row.Scan(
		&block.Hash,
		&block.Timestamp,
		&block.PreviousHash,
		&block.JournalHash,
		&block.Signature,
		&block.ContractID,
		&block.FunctionName,
		&block.Journal,
	)
	if err != nil {
		return nil, err
	}

	return &block, nil
}

func (r *PsqlBlockRepository) GetLastContractBlock(ctx context.Context, contractId string) (*schema.Block, error) {
	query := `
		SELECT block_index, hash, timestamp, previous_hash, journal_hash,
		       signature, contract_id, function_name, journal, status, failed_reason
		FROM blocks
		WHERE contract_id = $1
		ORDER BY timestamp DESC
		LIMIT 1
	`
	row := r.db.QueryRowContext(ctx, query, contractId)

	var block schema.Block
	err := row.Scan(
		&block.BlockIndex,
		&block.Hash,
		&block.Timestamp,
		&block.PreviousHash,
		&block.JournalHash,
		&block.Signature,
		&block.ContractID,
		&block.FunctionName,
		&block.Journal,
		&block.Status,
		&block.FailedReason,
	)

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			slog.Info("No blocks found in database, creating genesis block")
			return r.createGenesisBlock(ctx, contractId)
		}
		return nil, err
	}

	return &block, nil
}

func (r *PsqlBlockRepository) GetFinalBlockByContextID(ctx context.Context, contextID string) (*schema.Block, error) {
	query := `
        SELECT block_index, hash, previous_hash, journal_hash, timestamp,
               function_name, contract_id, context_id, status, failed_reason
        FROM blocks
        WHERE TRIM(context_id) = $1
        AND (function_name = 'pow' OR status = 'rejected')
        ORDER BY block_index DESC
        LIMIT 1
    `
	row := r.db.QueryRowContext(ctx, query, strings.TrimSpace(contextID))

	var block schema.Block
	err := row.Scan(
		&block.BlockIndex,
		&block.Hash,
		&block.PreviousHash,
		&block.JournalHash,
		&block.Timestamp,
		&block.FunctionName,
		&block.ContractID,
		&block.ContextID,
		&block.Status,
		&block.FailedReason,
	)

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}

	return &block, nil
}

func (r *PsqlBlockRepository) GetBlocksByContextID(ctx context.Context, contextID string) ([]*schema.Block, error) {
	query := `
		SELECT block_index, hash, previous_hash, journal_hash, timestamp,
		       function_name, contract_id, context_id, status, failed_reason
		FROM blocks
		WHERE context_id = $1
		ORDER BY block_index ASC
	`
	rows, err := r.db.QueryContext(ctx, query, contextID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var blocks []*schema.Block
	for rows.Next() {
		var block schema.Block
		if err := rows.Scan(
			&block.BlockIndex,
			&block.Hash,
			&block.PreviousHash,
			&block.JournalHash,
			&block.Timestamp,
			&block.FunctionName,
			&block.ContractID,
			&block.ContextID,
			&block.Status,
			&block.FailedReason,
		); err != nil {
			return nil, err
		}
		blocks = append(blocks, &block)
	}

	if len(blocks) == 0 {
		return nil, fmt.Errorf("context not found")
	}

	return blocks, nil
}

func (r *PsqlBlockRepository) createGenesisBlock(ctx context.Context, contractId string) (*schema.Block, error) {
	slog.Info("No blocks found in database, creating genesis block")
	genesis := &schema.Block{
		BlockIndex:   0,
		Hash:         "0x0000000000000000000000000000000000000000",
		Timestamp:    time.Now().Unix(),
		PreviousHash: "0",
		JournalHash:  "0",
		Signature:    []byte("GENESIS_SIGNATURE"),
		ContractID:   contractId,
		FunctionName: "genesis",
		Journal:      []byte{},
		Status:       "genesis",
		FailedReason: "",
	}

	query := `
		INSERT INTO blocks (
			block_index, hash, timestamp, previous_hash, journal_hash,
			signature, contract_id, function_name, journal, status, failed_reason
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`

	_, err := r.db.ExecContext(ctx, query,
		genesis.BlockIndex,
		genesis.Hash,
		genesis.Timestamp,
		genesis.PreviousHash,
		genesis.JournalHash,
		genesis.Signature,
		genesis.ContractID,
		genesis.FunctionName,
		genesis.Journal,
		genesis.Status,
		genesis.FailedReason,
	)
	if err != nil {
		return nil, err
	}

	return genesis, nil
}
