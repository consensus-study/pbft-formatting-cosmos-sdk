// Package pbft provides PBFT consensus engine.
package pbft

import (
	"context"
	"sync"

	abcitypes "github.com/cometbft/cometbft/abci/types"

	"github.com/ahwlsqja/pbft-cosmos/abci"
	"github.com/ahwlsqja/pbft-cosmos/types"
)

// LocalABCIAdapter는 내장 Application과 직접 연결하는 어댑터
// 테스트 및 단일 프로세스 실행에 사용
// ABCIAdapterInterface를 구현
type LocalABCIAdapter struct {
	mu           sync.RWMutex
	app          *abci.Application
	lastAppHash  []byte
	lastHeight   int64
	pendingBlock *types.Block // FinalizeBlock에서 처리 중인 블록
}

// NewLocalABCIAdapter creates a new local ABCI adapter with the given application.
func NewLocalABCIAdapter(app *abci.Application) *LocalABCIAdapter {
	return &LocalABCIAdapter{
		app: app,
	}
}

// Close closes the adapter.
func (a *LocalABCIAdapter) Close() error {
	return nil
}

// InitChain initializes the chain.
func (a *LocalABCIAdapter) InitChain(ctx context.Context, chainID string, validators []*types.Validator, appState []byte) error {
	if err := a.app.InitChain(validators); err != nil {
		return err
	}
	a.mu.Lock()
	a.lastAppHash = a.app.GetAppHash()
	a.lastHeight = 0
	a.mu.Unlock()
	return nil
}

// PrepareProposal prepares a block proposal (returns txs as-is for simple app).
func (a *LocalABCIAdapter) PrepareProposal(ctx context.Context, height int64, proposer []byte, txs [][]byte) ([][]byte, error) {
	// 간단한 구현: 트랜잭션을 그대로 반환
	// 실제 구현에서는 앱이 트랜잭션 정렬/필터링 수행
	return txs, nil
}

// ProcessProposal validates a block proposal.
func (a *LocalABCIAdapter) ProcessProposal(ctx context.Context, height int64, proposer []byte, txs [][]byte, hash []byte) (bool, error) {
	// 간단한 구현: 항상 수락
	// 실제 구현에서는 앱이 블록 유효성 검증
	return true, nil
}

// FinalizeBlock executes all transactions in a block.
func (a *LocalABCIAdapter) FinalizeBlock(ctx context.Context, block *types.Block) (*ABCIExecutionResult, error) {
	// BeginBlock
	if err := a.app.BeginBlock(block.Header.Height, block.Header.ProposerID); err != nil {
		return nil, err
	}

	// 블록 실행
	appHash, err := a.app.ExecuteBlock(block)
	if err != nil {
		return nil, err
	}

	// EndBlock
	endResp, err := a.app.EndBlock(block.Header.Height)
	if err != nil {
		return nil, err
	}

	// 트랜잭션 결과 생성
	txResults := make([]ABCITxResult, len(block.Transactions))
	for i := range block.Transactions {
		txResults[i] = ABCITxResult{
			Code: 0,
			Log:  "success",
		}
	}

	a.mu.Lock()
	a.lastAppHash = appHash
	a.pendingBlock = block // 커밋 대기 중인 블록 저장
	a.mu.Unlock()

	// ValidatorUpdates 변환
	var validatorUpdates []abcitypes.ValidatorUpdate
	if endResp.ValidatorUpdates != nil {
		validatorUpdates = make([]abcitypes.ValidatorUpdate, len(endResp.ValidatorUpdates))
		for i, v := range endResp.ValidatorUpdates {
			validatorUpdates[i] = abcitypes.ValidatorUpdate{
				Power: v.Power,
			}
		}
	}

	return &ABCIExecutionResult{
		TxResults:        txResults,
		AppHash:          appHash,
		ValidatorUpdates: validatorUpdates,
	}, nil
}

// Commit commits the current state.
func (a *LocalABCIAdapter) Commit(ctx context.Context) (appHash []byte, retainHeight int64, err error) {
	a.mu.Lock()
	block := a.pendingBlock
	a.pendingBlock = nil
	a.mu.Unlock()

	// FinalizeBlock에서 저장한 블록이 있으면 커밋
	if block != nil {
		if err := a.app.Commit(block); err != nil {
			return nil, 0, err
		}
	}

	a.mu.Lock()
	a.lastHeight = int64(a.app.GetHeight())
	a.lastAppHash = a.app.GetAppHash()
	appHash = a.lastAppHash
	a.mu.Unlock()

	return appHash, 0, nil
}

// CheckTx validates a transaction before adding to mempool.
func (a *LocalABCIAdapter) CheckTx(ctx context.Context, tx []byte) error {
	// 간단한 검증: 비어있지 않은지 확인
	if len(tx) == 0 {
		return localAdapterError("transaction is empty")
	}
	return nil
}

// GetLastAppHash returns the last app hash.
func (a *LocalABCIAdapter) GetLastAppHash() []byte {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.lastAppHash
}

// GetLastHeight returns the last committed height.
func (a *LocalABCIAdapter) GetLastHeight() int64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.lastHeight
}

// SetLastAppHash sets the last app hash.
func (a *LocalABCIAdapter) SetLastAppHash(hash []byte) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.lastAppHash = hash
}

// GetApplication returns the underlying application (for testing).
func (a *LocalABCIAdapter) GetApplication() *abci.Application {
	return a.app
}

// localAdapterError is an error type for local adapter.
type localAdapterError string

func (e localAdapterError) Error() string {
	return string(e)
}
