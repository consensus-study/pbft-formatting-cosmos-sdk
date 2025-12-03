// Package pbft provides the PBFT consensus engine with ABCI 2.0 integration.
package pbft

import (
	"context"
	"fmt"
	"sync"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/cometbft/cometbft/proto/tendermint/crypto"

	abciclient "github.com/ahwlsqja/pbft-cosmos/abci"
	"github.com/ahwlsqja/pbft-cosmos/types"
)

// ABCIAdapter - PBFT 엔진과 ABCI 앱 사이의 어댑터
// CometBFT v0.38.x ABCI 2.0 인터페이스 사용
type ABCIAdapter struct {
	mu     sync.RWMutex
	client *abciclient.Client // ABCI 클라이언트

	// 현재 상태
	lastHeight  int64 // 마지막 커밋된 높이
	lastAppHash []byte // 마지막 앱 해시

	// 설정
	maxTxBytes int64 // 최대 트랜잭션 크기 (기본1MB)
	chainID    string // 체인 ID
}

// NewABCIAdapter - ABCI 어댑터 생성
func NewABCIAdapter(abciAddress string) (*ABCIAdapter, error) {
	config := abciclient.DefaultClientConfig(abciAddress)
	client, err := abciclient.NewClient(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create ABCI client: %w", err)
	}

	return &ABCIAdapter{
		client:     client,
		maxTxBytes: 1024 * 1024, // 1MB 기본값
	}, nil
}

// Close - 어댑터 종료
func (a *ABCIAdapter) Close() error {
	if a.client != nil {
		return a.client.Close()
	}
	return nil
}

// InitChain - 체인 초기화
func (a *ABCIAdapter) InitChain(ctx context.Context, chainID string, validators []*types.Validator, appState []byte) error {
	a.chainID = chainID

	// Validator를 ABCI 타입으로 변환
	abciValidators := make([]abci.ValidatorUpdate, len(validators))
	for i, v := range validators {
		abciValidators[i] = abci.ValidatorUpdate{
			PubKey: crypto.PublicKey{
				Sum: &crypto.PublicKey_Ed25519{Ed25519: v.PublicKey},
			},
			Power: v.Power,
		}
	}

	req := &abci.RequestInitChain{
		ChainId:       chainID,
		Validators:    abciValidators,
		Time:          time.Now(),
		AppStateBytes: appState,
		InitialHeight: 1,
	}

	resp, err := a.client.InitChain(ctx, req)
	if err != nil {
		return fmt.Errorf("InitChain failed: %w", err)
	}

	a.mu.Lock()
	a.lastAppHash = resp.AppHash
	a.lastHeight = 0
	a.mu.Unlock()

	return nil
}

// PrepareProposal - 블록 제안 준비 (리더만 호출)
// ABCI 앱에게 트랜잭션 정렬/필터링 요청
func (a *ABCIAdapter) PrepareProposal(ctx context.Context, height int64, proposer []byte, txs [][]byte) ([][]byte, error) {
	req := abciclient.NewPrepareProposalRequest(
		txs,
		a.maxTxBytes,
		height,
		time.Now(),
		proposer,
	)

	resp, err := a.client.PrepareProposal(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("PrepareProposal failed: %w", err)
	}

	return resp.Txs, nil
}

// ProcessProposal - 블록 제안 검증 (PrePrepare 수신 시)
// 리턴: true=ACCEPT, false=REJECT
func (a *ABCIAdapter) ProcessProposal(ctx context.Context, height int64, proposer []byte, txs [][]byte, hash []byte) (bool, error) {
	req := abciclient.NewProcessProposalRequest(
		txs,
		height,
		time.Now(),
		proposer,
		hash,
	)

	resp, err := a.client.ProcessProposal(ctx, req)
	if err != nil {
		return false, fmt.Errorf("ProcessProposal failed: %w", err)
	}

	return resp.Status == abci.ResponseProcessProposal_ACCEPT, nil
}

// FinalizeBlock - 블록 실행 (Commit 단계 후)
// ABCI 2.0: BeginBlock + DeliverTx + EndBlock 통합
func (a *ABCIAdapter) FinalizeBlock(ctx context.Context, block *types.Block) (*ABCIExecutionResult, error) {
	// 트랜잭션 추출
	txs := make([][]byte, len(block.Transactions))
	for i, tx := range block.Transactions {
		txs[i] = tx.Data
	}

	blockData := &abciclient.BlockData{
		Height:       int64(block.Header.Height),
		Txs:          txs,
		Hash:         block.Hash,
		Time:         block.Header.Timestamp,
		ProposerAddr: []byte(block.Header.ProposerID),
	}

	req := abciclient.NewFinalizeBlockRequest(blockData)

	resp, err := a.client.FinalizeBlock(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("FinalizeBlock failed: %w", err)
	}

	// 결과 변환
	result := abciclient.FinalizeBlockResponseToResult(resp)

	return &ABCIExecutionResult{
		TxResults:        convertTxResults(result.TxResults),
		ValidatorUpdates: result.ValidatorUpdates,
		AppHash:          result.AppHash,
		Events:           result.Events,
	}, nil
}

// Commit - 상태 커밋
func (a *ABCIAdapter) Commit(ctx context.Context) (appHash []byte, retainHeight int64, err error) {
	resp, err := a.client.Commit(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("Commit failed: %w", err)
	}

	a.mu.Lock()
	a.lastHeight++
	a.mu.Unlock()

	return a.lastAppHash, resp.RetainHeight, nil
}

// CheckTx - 트랜잭션 유효성 검사 (멤풀 진입 전)
func (a *ABCIAdapter) CheckTx(ctx context.Context, tx []byte) error {
	resp, err := a.client.CheckTx(ctx, tx)
	if err != nil {
		return err
	}

	if resp.Code != 0 {
		return fmt.Errorf("CheckTx failed (code=%d): %s", resp.Code, resp.Log)
	}

	return nil
}

// Query - 상태 쿼리
func (a *ABCIAdapter) Query(ctx context.Context, path string, data []byte, height int64, prove bool) (*ABCIQueryResult, error) {
	resp, err := a.client.Query(ctx, &abci.RequestQuery{
		Path:   path,
		Data:   data,
		Height: height,
		Prove:  prove,
	})
	if err != nil {
		return nil, err
	}

	if resp.Code != 0 {
		return nil, fmt.Errorf("query failed (code=%d): %s", resp.Code, resp.Log)
	}

	return &ABCIQueryResult{
		Key:    resp.Key,
		Value:  resp.Value,
		Height: resp.Height,
	}, nil
}

// Info - 앱 정보 조회
func (a *ABCIAdapter) Info(ctx context.Context) (*ABCIInfo, error) {
	resp, err := a.client.Info(ctx)
	if err != nil {
		return nil, err
	}

	a.mu.Lock()
	a.lastHeight = resp.LastBlockHeight
	a.lastAppHash = resp.LastBlockAppHash
	a.mu.Unlock()

	return &ABCIInfo{
		Version:          resp.Version,
		AppVersion:       resp.AppVersion,
		LastBlockHeight:  resp.LastBlockHeight,
		LastBlockAppHash: resp.LastBlockAppHash,
	}, nil
}

// GetLastAppHash - 마지막 앱 해시 반환
func (a *ABCIAdapter) GetLastAppHash() []byte {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.lastAppHash
}

// GetLastHeight - 마지막 높이 반환
func (a *ABCIAdapter) GetLastHeight() int64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.lastHeight
}

// SetLastAppHash - 앱 해시 설정
func (a *ABCIAdapter) SetLastAppHash(hash []byte) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.lastAppHash = hash
}

// SetMaxTxBytes - 최대 트랜잭션 바이트 설정
func (a *ABCIAdapter) SetMaxTxBytes(maxBytes int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.maxTxBytes = maxBytes
}

// ABCIExecutionResult - 블록 실행 결과
type ABCIExecutionResult struct {
	TxResults        []ABCITxResult // 각 트랜잭션 실행 결과들
	ValidatorUpdates []abci.ValidatorUpdate //  검증자 변경사항
	AppHash          []byte // 새 앱 상태 해시 머클 루트임
	Events           []abci.Event // 발생한 이벤트들
}

// ABCITxResult - 트랜잭션 실행 결과
type ABCITxResult struct {
	Code      uint32 // 결과 코드 (0=성공)
	Data      []byte // 반환 데이터
	Log       string // 로그 메시지
	Info      string // 추가 정보
	GasWanted int64 // 요청 가스
	GasUsed   int64 // 사용 가스
}

// ABCIQueryResult - 쿼리 결과
type ABCIQueryResult struct {
	Key    []byte // 쿼리 키
	Value  []byte // 결과 값
	Height int64 // 쿼리 높이
}

// ABCIInfo - 앱 정보
type ABCIInfo struct {
	Version          string // 앱 버전
	AppVersion       uint64 // 앱 버전 번호
	LastBlockHeight  int64 // 마지막 블록 높이
	LastBlockAppHash []byte // 마지막 앱 해시
}

// convertTxResults - 트랜잭션 결과 변환
func convertTxResults(results []abciclient.TxResult) []ABCITxResult {
	converted := make([]ABCITxResult, len(results))
	for i, r := range results {
		converted[i] = ABCITxResult{
			Code:      r.Code,
			Data:      r.Data,
			Log:       r.Log,
			Info:      r.Info,
			GasWanted: r.GasWanted,
			GasUsed:   r.GasUsed,
		}
	}
	return converted
}

// MockABCIAdapter - 테스트용 Mock 어댑터
type MockABCIAdapter struct {
	mu          sync.RWMutex
	lastHeight  int64
	lastAppHash []byte
	state       map[string][]byte
}

// NewMockABCIAdapter - Mock 어댑터 생성
func NewMockABCIAdapter() *MockABCIAdapter {
	return &MockABCIAdapter{
		state: make(map[string][]byte),
	}
}

func (m *MockABCIAdapter) Close() error { return nil }

func (m *MockABCIAdapter) InitChain(ctx context.Context, chainID string, validators []*types.Validator, appState []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastHeight = 0
	m.lastAppHash = make([]byte, 32)
	return nil
}

func (m *MockABCIAdapter) PrepareProposal(ctx context.Context, height int64, proposer []byte, txs [][]byte) ([][]byte, error) {
	return txs, nil
}

func (m *MockABCIAdapter) ProcessProposal(ctx context.Context, height int64, proposer []byte, txs [][]byte, hash []byte) (bool, error) {
	return true, nil
}

func (m *MockABCIAdapter) FinalizeBlock(ctx context.Context, block *types.Block) (*ABCIExecutionResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	txResults := make([]ABCITxResult, len(block.Transactions))
	for i := range block.Transactions {
		txResults[i] = ABCITxResult{Code: 0, Log: "success"}
	}

	m.lastAppHash = block.Hash
	return &ABCIExecutionResult{
		TxResults: txResults,
		AppHash:   m.lastAppHash,
	}, nil
}

func (m *MockABCIAdapter) Commit(ctx context.Context) ([]byte, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastHeight++
	return m.lastAppHash, 0, nil
}

func (m *MockABCIAdapter) CheckTx(ctx context.Context, tx []byte) error {
	return nil
}

func (m *MockABCIAdapter) Query(ctx context.Context, path string, data []byte, height int64, prove bool) (*ABCIQueryResult, error) {
	return &ABCIQueryResult{}, nil
}

func (m *MockABCIAdapter) GetLastAppHash() []byte {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.lastAppHash
}

func (m *MockABCIAdapter) GetLastHeight() int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.lastHeight
}

func (m *MockABCIAdapter) SetLastAppHash(hash []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastAppHash = hash
}
