// Package pbft provides PBFT consensus engine.
package pbft

import (
	"context"

	"github.com/ahwlsqja/pbft-cosmos/types"
)

// NoopABCIAdapter - ABCI 연결 없이 작동하는 어댑터
// 테스트나 ABCI 앱 없이 노드를 실행할 때 사용
type NoopABCIAdapter struct {
	lastAppHash []byte
	lastHeight  int64
}

// NewNoopABCIAdapter creates a new NoopABCIAdapter.
func NewNoopABCIAdapter() *NoopABCIAdapter {
	return &NoopABCIAdapter{
		lastAppHash: make([]byte, 32),
		lastHeight:  0,
	}
}

// Close - 연결 종료 (noop)
func (a *NoopABCIAdapter) Close() error {
	return nil
}

// InitChain - 체인 초기화 (noop)
func (a *NoopABCIAdapter) InitChain(ctx context.Context, chainID string, validators []*types.Validator, appState []byte) error {
	return nil
}

// PrepareProposal - 트랜잭션을 그대로 반환
func (a *NoopABCIAdapter) PrepareProposal(ctx context.Context, height int64, proposer []byte, txs [][]byte) ([][]byte, error) {
	// 트랜잭션을 그대로 반환 (정렬/필터링 없음)
	return txs, nil
}

// ProcessProposal - 항상 수락
func (a *NoopABCIAdapter) ProcessProposal(ctx context.Context, height int64, proposer []byte, txs [][]byte, hash []byte) (bool, error) {
	return true, nil
}

// FinalizeBlock - 블록 실행 (noop)
func (a *NoopABCIAdapter) FinalizeBlock(ctx context.Context, block *types.Block) (*ABCIExecutionResult, error) {
	txResults := make([]ABCITxResult, len(block.Transactions))
	for i := range block.Transactions {
		txResults[i] = ABCITxResult{
			Code: 0,
			Data: nil,
			Log:  "success",
		}
	}

	a.lastHeight = int64(block.Header.Height)

	return &ABCIExecutionResult{
		TxResults:        txResults,
		AppHash:          a.lastAppHash,
		ValidatorUpdates: nil,
	}, nil
}

// Commit - 커밋 (noop)
func (a *NoopABCIAdapter) Commit(ctx context.Context) (appHash []byte, retainHeight int64, err error) {
	return a.lastAppHash, 0, nil
}

// CheckTx - 트랜잭션 검증 (항상 성공)
func (a *NoopABCIAdapter) CheckTx(ctx context.Context, tx []byte) error {
	return nil
}

// GetLastAppHash - 마지막 앱 해시 반환
func (a *NoopABCIAdapter) GetLastAppHash() []byte {
	return a.lastAppHash
}

// GetLastHeight - 마지막 높이 반환
func (a *NoopABCIAdapter) GetLastHeight() int64 {
	return a.lastHeight
}

// SetLastAppHash - 앱 해시 설정
func (a *NoopABCIAdapter) SetLastAppHash(hash []byte) {
	a.lastAppHash = hash
}
