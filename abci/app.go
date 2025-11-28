// Package abci provides the ABCI++ interface implementation for connecting
// the PBFT consensus engine to the Cosmos SDK application layer.
package abci

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/ahwlsqja/pbft-cosmos/types"
)

// Application represents a simple key-value store application
// that implements the ABCI interface for the PBFT consensus engine.
type Application struct {
	mu sync.RWMutex

	// State - simple key-value store
	state map[string][]byte

	// Committed state (after block is finalized)
	committedState map[string][]byte

	// Pending transactions
	pendingTxs []types.Transaction

	// Block history
	blockHistory []*types.Block

	// Current height
	height uint64

	// App hash (state root)
	appHash []byte
}

// NewApplication creates a new ABCI application.
func NewApplication() *Application {
	return &Application{
		state:          make(map[string][]byte),
		committedState: make(map[string][]byte),
		pendingTxs:     make([]types.Transaction, 0),
		blockHistory:   make([]*types.Block, 0),
		height:         0,
	}
}

// ExecuteBlock executes all transactions in a block.
func (app *Application) ExecuteBlock(block *types.Block) ([]byte, error) {
	app.mu.Lock()
	defer app.mu.Unlock()

	// Execute each transaction
	for _, tx := range block.Transactions {
		if err := app.executeTx(tx); err != nil {
			return nil, fmt.Errorf("failed to execute tx %s: %w", tx.ID, err)
		}
	}

	// Compute new app hash
	app.appHash = app.computeAppHash()

	return app.appHash, nil
}

// executeTx executes a single transaction.
func (app *Application) executeTx(tx types.Transaction) error {
	// Parse transaction data as a simple key-value operation
	var op Operation
	if err := json.Unmarshal(tx.Data, &op); err != nil {
		// If not JSON, treat as raw data with tx ID as key
		app.state[tx.ID] = tx.Data
		return nil
	}

	switch op.Type {
	case "set":
		app.state[op.Key] = []byte(op.Value)
	case "delete":
		delete(app.state, op.Key)
	default:
		return fmt.Errorf("unknown operation type: %s", op.Type)
	}

	return nil
}

// Operation represents a key-value operation.
type Operation struct {
	Type  string `json:"type"`  // "set" or "delete"
	Key   string `json:"key"`
	Value string `json:"value,omitempty"`
}

// ValidateBlock validates a block before consensus.
func (app *Application) ValidateBlock(block *types.Block) error {
	if block == nil {
		return fmt.Errorf("block is nil")
	}

	// Validate each transaction
	for _, tx := range block.Transactions {
		if err := app.validateTx(tx); err != nil {
			return fmt.Errorf("invalid tx %s: %w", tx.ID, err)
		}
	}

	return nil
}

// validateTx validates a single transaction.
func (app *Application) validateTx(tx types.Transaction) error {
	if tx.ID == "" {
		return fmt.Errorf("transaction ID is empty")
	}

	if len(tx.Data) == 0 {
		return fmt.Errorf("transaction data is empty")
	}

	// Additional validation can be added here
	// e.g., signature verification, nonce checking, etc.

	return nil
}

// GetPendingTransactions returns pending transactions for a new block.
func (app *Application) GetPendingTransactions() []types.Transaction {
	app.mu.Lock()
	defer app.mu.Unlock()

	txs := make([]types.Transaction, len(app.pendingTxs))
	copy(txs, app.pendingTxs)
	app.pendingTxs = make([]types.Transaction, 0)

	return txs
}

// Commit commits the current state.
func (app *Application) Commit(block *types.Block) error {
	app.mu.Lock()
	defer app.mu.Unlock()

	// Copy state to committed state
	app.committedState = make(map[string][]byte)
	for k, v := range app.state {
		app.committedState[k] = v
	}

	// Update height
	app.height = block.Header.Height

	// Add to block history
	app.blockHistory = append(app.blockHistory, block)

	return nil
}

// AddPendingTransaction adds a transaction to the pending pool.
func (app *Application) AddPendingTransaction(tx types.Transaction) error {
	app.mu.Lock()
	defer app.mu.Unlock()

	// Validate transaction
	if err := app.validateTx(tx); err != nil {
		return err
	}

	app.pendingTxs = append(app.pendingTxs, tx)
	return nil
}

// Query queries the application state.
func (app *Application) Query(key string) ([]byte, error) {
	app.mu.RLock()
	defer app.mu.RUnlock()

	value, exists := app.committedState[key]
	if !exists {
		return nil, fmt.Errorf("key not found: %s", key)
	}

	return value, nil
}

// GetHeight returns the current height.
func (app *Application) GetHeight() uint64 {
	app.mu.RLock()
	defer app.mu.RUnlock()
	return app.height
}

// GetAppHash returns the current app hash.
func (app *Application) GetAppHash() []byte {
	app.mu.RLock()
	defer app.mu.RUnlock()
	return app.appHash
}

// GetBlockHistory returns the block history.
func (app *Application) GetBlockHistory() []*types.Block {
	app.mu.RLock()
	defer app.mu.RUnlock()
	return app.blockHistory
}

// computeAppHash computes the state root hash.
func (app *Application) computeAppHash() []byte {
	// Simple hash computation - in production, use a Merkle tree
	data, _ := json.Marshal(app.state)
	hash := make([]byte, 32)
	for i, b := range data {
		hash[i%32] ^= b
	}
	return hash
}

// Info returns application info (ABCI Info).
type Info struct {
	Version          string `json:"version"`
	AppVersion       uint64 `json:"app_version"`
	LastBlockHeight  uint64 `json:"last_block_height"`
	LastBlockAppHash []byte `json:"last_block_app_hash"`
}

// GetInfo returns the application info.
func (app *Application) GetInfo() *Info {
	app.mu.RLock()
	defer app.mu.RUnlock()

	return &Info{
		Version:          "1.0.0",
		AppVersion:       1,
		LastBlockHeight:  app.height,
		LastBlockAppHash: app.appHash,
	}
}

// InitChain initializes the blockchain (ABCI InitChain).
func (app *Application) InitChain(validators []*types.Validator) error {
	app.mu.Lock()
	defer app.mu.Unlock()

	// Initialize genesis state
	app.state = make(map[string][]byte)
	app.committedState = make(map[string][]byte)
	app.height = 0
	app.appHash = app.computeAppHash()

	return nil
}

// BeginBlock signals the beginning of a new block (ABCI BeginBlock).
func (app *Application) BeginBlock(height uint64, proposer string) error {
	// Can be used to update state based on block metadata
	return nil
}

// EndBlock signals the end of a block (ABCI EndBlock).
func (app *Application) EndBlock(height uint64) (*EndBlockResponse, error) {
	// Can return validator updates
	return &EndBlockResponse{
		ValidatorUpdates: nil,
	}, nil
}

// EndBlockResponse contains the response for EndBlock.
type EndBlockResponse struct {
	ValidatorUpdates []*types.Validator `json:"validator_updates"`
}

// CheckTx validates a transaction before adding to mempool (ABCI CheckTx).
func (app *Application) CheckTx(tx types.Transaction) error {
	return app.validateTx(tx)
}

// DeliverTx executes a transaction (ABCI DeliverTx).
func (app *Application) DeliverTx(tx types.Transaction) (*DeliverTxResponse, error) {
	app.mu.Lock()
	defer app.mu.Unlock()

	if err := app.executeTx(tx); err != nil {
		return &DeliverTxResponse{
			Code: 1,
			Log:  err.Error(),
		}, err
	}

	return &DeliverTxResponse{
		Code: 0,
		Log:  "success",
	}, nil
}

// DeliverTxResponse contains the response for DeliverTx.
type DeliverTxResponse struct {
	Code uint32 `json:"code"`
	Log  string `json:"log"`
	Data []byte `json:"data,omitempty"`
}

// Snapshot returns state snapshot for state sync (ABCI ListSnapshots/OfferSnapshot).
func (app *Application) Snapshot() ([]byte, error) {
	app.mu.RLock()
	defer app.mu.RUnlock()

	return json.Marshal(app.committedState)
}

// RestoreSnapshot restores state from a snapshot.
func (app *Application) RestoreSnapshot(data []byte) error {
	app.mu.Lock()
	defer app.mu.Unlock()

	var state map[string][]byte
	if err := json.Unmarshal(data, &state); err != nil {
		return err
	}

	app.state = state
	app.committedState = make(map[string][]byte)
	for k, v := range state {
		app.committedState[k] = v
	}

	return nil
}
