// Package persistence provides state synchronization for new nodes.
// 새로운 노드가 네트워크에 합류할 때 상태를 동기화하는 기능
package persistence

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/ahwlsqja/pbft-cosmos/types"
)

// ================================================================================
//                          State Sync 인터페이스
// ================================================================================

// BlockProvider는 블록을 제공하는 인터페이스 (다른 노드로부터)
type BlockProvider interface {
	// GetBlocks는 지정된 높이 범위의 블록을 요청
	GetBlocks(ctx context.Context, fromHeight, toHeight uint64) ([]*types.Block, error)
	// GetLatestHeight는 네트워크의 최신 높이를 반환
	GetLatestHeight(ctx context.Context) (uint64, error)
	// GetCheckpoints는 체크포인트 목록을 반환
	GetCheckpoints(ctx context.Context) ([]*Checkpoint, error)
}

// StateSyncer는 상태 동기화를 관리
type StateSyncer struct {
	mu sync.RWMutex

	store         Store          // 저장소
	blockProvider BlockProvider  // 블록 제공자
	logger        *log.Logger    // 로거

	// 동기화 상태
	syncing       bool           // 동기화 중 여부
	targetHeight  uint64         // 목표 높이
	currentHeight uint64         // 현재 높이

	// 콜백
	onBlockReceived func(*types.Block) error // 블록 수신 시 콜백
	onSyncComplete  func()                   // 동기화 완료 시 콜백
}

// NewStateSyncer creates a new state syncer.
func NewStateSyncer(store Store, provider BlockProvider, logger *log.Logger) *StateSyncer {
	if logger == nil {
		logger = log.Default()
	}
	return &StateSyncer{
		store:         store,
		blockProvider: provider,
		logger:        logger,
	}
}

// SetOnBlockReceived sets the callback for when a block is received.
func (ss *StateSyncer) SetOnBlockReceived(callback func(*types.Block) error) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	ss.onBlockReceived = callback
}

// SetOnSyncComplete sets the callback for when sync is complete.
func (ss *StateSyncer) SetOnSyncComplete(callback func()) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	ss.onSyncComplete = callback
}

// ================================================================================
//                          동기화 메서드
// ================================================================================

// Sync starts the synchronization process.
// 현재 높이에서 네트워크의 최신 높이까지 블록을 동기화
func (ss *StateSyncer) Sync(ctx context.Context) error {
	ss.mu.Lock()
	if ss.syncing {
		ss.mu.Unlock()
		return fmt.Errorf("sync already in progress")
	}
	ss.syncing = true
	ss.mu.Unlock()

	defer func() {
		ss.mu.Lock()
		ss.syncing = false
		ss.mu.Unlock()
	}()

	// 1. 현재 로컬 높이 확인
	localHeight, err := ss.store.GetLatestBlockHeight()
	if err != nil {
		return fmt.Errorf("failed to get local height: %w", err)
	}

	ss.mu.Lock()
	ss.currentHeight = localHeight
	ss.mu.Unlock()

	ss.logger.Printf("[StateSync] Starting sync from height %d", localHeight)

	// 2. 네트워크 최신 높이 확인
	targetHeight, err := ss.blockProvider.GetLatestHeight(ctx)
	if err != nil {
		return fmt.Errorf("failed to get target height: %w", err)
	}

	ss.mu.Lock()
	ss.targetHeight = targetHeight
	ss.mu.Unlock()

	if localHeight >= targetHeight {
		ss.logger.Printf("[StateSync] Already at latest height %d", localHeight)
		return nil
	}

	ss.logger.Printf("[StateSync] Target height: %d, need to sync %d blocks",
		targetHeight, targetHeight-localHeight)

	// 3. 블록 배치로 동기화
	const batchSize = 100
	for height := localHeight + 1; height <= targetHeight; {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// 배치 범위 계산
		endHeight := height + batchSize - 1
		if endHeight > targetHeight {
			endHeight = targetHeight
		}

		// 블록 요청
		blocks, err := ss.blockProvider.GetBlocks(ctx, height, endHeight)
		if err != nil {
			return fmt.Errorf("failed to get blocks %d-%d: %w", height, endHeight, err)
		}

		// 블록 처리
		for _, block := range blocks {
			if err := ss.processBlock(block); err != nil {
				return fmt.Errorf("failed to process block %d: %w", block.Header.Height, err)
			}
		}

		ss.mu.Lock()
		ss.currentHeight = endHeight
		ss.mu.Unlock()

		ss.logger.Printf("[StateSync] Synced blocks %d-%d", height, endHeight)

		height = endHeight + 1
	}

	ss.logger.Printf("[StateSync] Sync complete at height %d", targetHeight)

	// 동기화 완료 콜백
	ss.mu.RLock()
	callback := ss.onSyncComplete
	ss.mu.RUnlock()
	if callback != nil {
		callback()
	}

	return nil
}

// processBlock processes a single block during sync.
func (ss *StateSyncer) processBlock(block *types.Block) error {
	// 블록 저장
	if err := ss.store.SaveBlock(block); err != nil {
		return fmt.Errorf("failed to save block: %w", err)
	}

	// 콜백 호출 (앱 상태 업데이트 등)
	ss.mu.RLock()
	callback := ss.onBlockReceived
	ss.mu.RUnlock()
	if callback != nil {
		if err := callback(block); err != nil {
			return fmt.Errorf("block callback failed: %w", err)
		}
	}

	return nil
}

// SyncFromCheckpoint syncs from the latest checkpoint.
// 체크포인트부터 동기화 (더 빠름)
func (ss *StateSyncer) SyncFromCheckpoint(ctx context.Context) error {
	ss.mu.Lock()
	if ss.syncing {
		ss.mu.Unlock()
		return fmt.Errorf("sync already in progress")
	}
	ss.syncing = true
	ss.mu.Unlock()

	defer func() {
		ss.mu.Lock()
		ss.syncing = false
		ss.mu.Unlock()
	}()

	// 1. 네트워크에서 체크포인트 가져오기
	checkpoints, err := ss.blockProvider.GetCheckpoints(ctx)
	if err != nil {
		return fmt.Errorf("failed to get checkpoints: %w", err)
	}

	if len(checkpoints) == 0 {
		ss.logger.Printf("[StateSync] No checkpoints found, syncing from genesis")
		return ss.Sync(ctx)
	}

	// 2. 가장 최근 체크포인트 찾기
	var latestCheckpoint *Checkpoint
	for _, cp := range checkpoints {
		if latestCheckpoint == nil || cp.SequenceNum > latestCheckpoint.SequenceNum {
			latestCheckpoint = cp
		}
	}

	ss.logger.Printf("[StateSync] Found checkpoint at height %d", latestCheckpoint.SequenceNum)

	// 3. 체크포인트 저장
	if err := ss.store.SaveCheckpoint(latestCheckpoint); err != nil {
		return fmt.Errorf("failed to save checkpoint: %w", err)
	}

	// 4. 체크포인트부터 최신까지 블록 동기화
	localHeight := latestCheckpoint.SequenceNum
	ss.mu.Lock()
	ss.currentHeight = localHeight
	ss.mu.Unlock()

	targetHeight, err := ss.blockProvider.GetLatestHeight(ctx)
	if err != nil {
		return fmt.Errorf("failed to get target height: %w", err)
	}

	if localHeight >= targetHeight {
		ss.logger.Printf("[StateSync] Checkpoint is at latest height")
		return nil
	}

	// 체크포인트 이후 블록만 동기화
	const batchSize = 100
	for height := localHeight + 1; height <= targetHeight; {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		endHeight := height + batchSize - 1
		if endHeight > targetHeight {
			endHeight = targetHeight
		}

		blocks, err := ss.blockProvider.GetBlocks(ctx, height, endHeight)
		if err != nil {
			return fmt.Errorf("failed to get blocks: %w", err)
		}

		for _, block := range blocks {
			if err := ss.processBlock(block); err != nil {
				return fmt.Errorf("failed to process block: %w", err)
			}
		}

		ss.mu.Lock()
		ss.currentHeight = endHeight
		ss.mu.Unlock()

		height = endHeight + 1
	}

	ss.logger.Printf("[StateSync] Sync from checkpoint complete at height %d", targetHeight)

	ss.mu.RLock()
	callback := ss.onSyncComplete
	ss.mu.RUnlock()
	if callback != nil {
		callback()
	}

	return nil
}

// ================================================================================
//                          상태 조회 메서드
// ================================================================================

// IsSyncing returns whether sync is in progress.
func (ss *StateSyncer) IsSyncing() bool {
	ss.mu.RLock()
	defer ss.mu.RUnlock()
	return ss.syncing
}

// GetProgress returns the sync progress.
func (ss *StateSyncer) GetProgress() (current, target uint64) {
	ss.mu.RLock()
	defer ss.mu.RUnlock()
	return ss.currentHeight, ss.targetHeight
}

// ================================================================================
//                          Recovery (복구)
// ================================================================================

// RecoveryManager는 노드 재시작 시 상태 복구를 관리
type RecoveryManager struct {
	store  Store
	logger *log.Logger
}

// NewRecoveryManager creates a new recovery manager.
func NewRecoveryManager(store Store, logger *log.Logger) *RecoveryManager {
	if logger == nil {
		logger = log.Default()
	}
	return &RecoveryManager{
		store:  store,
		logger: logger,
	}
}

// RecoveryResult는 복구 결과
type RecoveryResult struct {
	State        *ConsensusState // 복구된 상태
	Blocks       []*types.Block  // 복구된 블록들
	Checkpoints  []*Checkpoint   // 복구된 체크포인트들
	LatestHeight uint64          // 최신 블록 높이
}

// Recover recovers state from storage.
// 저장소에서 상태를 복구
func (rm *RecoveryManager) Recover() (*RecoveryResult, error) {
	rm.logger.Printf("[Recovery] Starting recovery...")
	startTime := time.Now()

	result := &RecoveryResult{}

	// 1. 상태 복구
	state, err := rm.store.LoadState()
	if err != nil {
		return nil, fmt.Errorf("failed to load state: %w", err)
	}
	result.State = state
	if state != nil {
		rm.logger.Printf("[Recovery] Loaded state: view=%d, height=%d",
			state.CurrentView, state.CurrentHeight)
	}

	// 2. 최신 블록 높이 확인
	latestHeight, err := rm.store.GetLatestBlockHeight()
	if err != nil {
		return nil, fmt.Errorf("failed to get latest height: %w", err)
	}
	result.LatestHeight = latestHeight
	rm.logger.Printf("[Recovery] Latest block height: %d", latestHeight)

	// 3. 체크포인트 복구
	checkpoints, err := rm.store.LoadCheckpoints()
	if err != nil {
		return nil, fmt.Errorf("failed to load checkpoints: %w", err)
	}
	result.Checkpoints = checkpoints
	rm.logger.Printf("[Recovery] Loaded %d checkpoints", len(checkpoints))

	// 4. 최근 블록들 복구 (마지막 10개)
	if latestHeight > 0 {
		fromHeight := uint64(1)
		if latestHeight > 10 {
			fromHeight = latestHeight - 9
		}
		blocks, err := rm.store.LoadBlocks(fromHeight, latestHeight)
		if err != nil {
			return nil, fmt.Errorf("failed to load blocks: %w", err)
		}
		result.Blocks = blocks
		rm.logger.Printf("[Recovery] Loaded %d recent blocks", len(blocks))
	}

	rm.logger.Printf("[Recovery] Recovery complete in %v", time.Since(startTime))
	return result, nil
}

// SaveRecoveryPoint saves the current state for recovery.
// 현재 상태를 저장 (주기적으로 호출)
func (rm *RecoveryManager) SaveRecoveryPoint(state *ConsensusState, block *types.Block) error {
	// 상태 저장
	if err := rm.store.SaveState(state); err != nil {
		return fmt.Errorf("failed to save state: %w", err)
	}

	// 블록 저장
	if block != nil {
		if err := rm.store.SaveBlock(block); err != nil {
			return fmt.Errorf("failed to save block: %w", err)
		}
	}

	return nil
}
