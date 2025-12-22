// Package persistence provides block and state storage for PBFT consensus.
// 블록과 상태를 영구 저장하고 복구하는 기능을 제공
package persistence

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/ahwlsqja/pbft-cosmos/types"
)

// Checkpoint는 체크포인트를 나타냄 (순환 참조 방지를 위해 별도 정의)
type Checkpoint struct {
	SequenceNum uint64 `json:"sequence_num"`
	Digest      []byte `json:"digest"`
	NodeID      string `json:"node_id"`
}

// Store는 블록과 상태를 저장하는 인터페이스
type Store interface {
	// 블록 관련
	SaveBlock(block *types.Block) error
	LoadBlock(height uint64) (*types.Block, error)
	LoadBlocks(fromHeight, toHeight uint64) ([]*types.Block, error)
	GetLatestBlockHeight() (uint64, error)

	// 상태 관련
	SaveState(state *ConsensusState) error
	LoadState() (*ConsensusState, error)

	// 체크포인트 관련
	SaveCheckpoint(checkpoint *Checkpoint) error
	LoadCheckpoint(seqNum uint64) (*Checkpoint, error)
	LoadCheckpoints() ([]*Checkpoint, error)
	GetLatestCheckpoint() (*Checkpoint, error)

	// 닫기
	Close() error
}

// ConsensusState는 합의 엔진의 영구 상태를 나타냄
type ConsensusState struct {
	NodeID        string `json:"node_id"`         // 노드 ID
	CurrentView   uint64 `json:"current_view"`    // 현재 뷰 번호
	CurrentHeight uint64 `json:"current_height"`  // 현재 블록 높이
	LastAppHash   []byte `json:"last_app_hash"`   // 마지막 앱 해시
	LastBlockHash []byte `json:"last_block_hash"` // 마지막 블록 해시
}

// ================================================================================
//                          File-based Store 구현
// ================================================================================

// FileStore는 파일 시스템 기반 저장소
type FileStore struct {
	mu      sync.RWMutex
	baseDir string // 기본 디렉토리
}

// NewFileStore creates a new file-based store.
func NewFileStore(baseDir string) (*FileStore, error) {
	// 디렉토리 생성
	dirs := []string{
		baseDir,
		filepath.Join(baseDir, "blocks"),
		filepath.Join(baseDir, "checkpoints"),
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	return &FileStore{
		baseDir: baseDir,
	}, nil
}

// ================================================================================
//                          블록 저장/로드
// ================================================================================

// SaveBlock saves a block to disk.
func (fs *FileStore) SaveBlock(block *types.Block) error {
	if block == nil {
		return fmt.Errorf("block is nil")
	}

	fs.mu.Lock()
	defer fs.mu.Unlock()

	data, err := json.MarshalIndent(block, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal block: %w", err)
	}

	filename := filepath.Join(fs.baseDir, "blocks", fmt.Sprintf("block_%d.json", block.Header.Height))
	if err := os.WriteFile(filename, data, 0644); err != nil {
		return fmt.Errorf("failed to write block file: %w", err)
	}

	return nil
}

// LoadBlock loads a block from disk.
func (fs *FileStore) LoadBlock(height uint64) (*types.Block, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	filename := filepath.Join(fs.baseDir, "blocks", fmt.Sprintf("block_%d.json", height))
	data, err := os.ReadFile(filename)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // 블록이 없으면 nil 반환
		}
		return nil, fmt.Errorf("failed to read block file: %w", err)
	}

	var block types.Block
	if err := json.Unmarshal(data, &block); err != nil {
		return nil, fmt.Errorf("failed to unmarshal block: %w", err)
	}

	return &block, nil
}

// LoadBlocks loads blocks in a range.
func (fs *FileStore) LoadBlocks(fromHeight, toHeight uint64) ([]*types.Block, error) {
	var blocks []*types.Block
	for h := fromHeight; h <= toHeight; h++ {
		block, err := fs.LoadBlock(h)
		if err != nil {
			return nil, err
		}
		if block != nil {
			blocks = append(blocks, block)
		}
	}
	return blocks, nil
}

// GetLatestBlockHeight returns the latest block height.
func (fs *FileStore) GetLatestBlockHeight() (uint64, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	blocksDir := filepath.Join(fs.baseDir, "blocks")
	entries, err := os.ReadDir(blocksDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("failed to read blocks directory: %w", err)
	}

	var maxHeight uint64
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		var height uint64
		if _, err := fmt.Sscanf(entry.Name(), "block_%d.json", &height); err == nil {
			if height > maxHeight {
				maxHeight = height
			}
		}
	}

	return maxHeight, nil
}

// ================================================================================
//                          상태 저장/로드
// ================================================================================

// SaveState saves the consensus state.
func (fs *FileStore) SaveState(state *ConsensusState) error {
	if state == nil {
		return fmt.Errorf("state is nil")
	}

	fs.mu.Lock()
	defer fs.mu.Unlock()

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	filename := filepath.Join(fs.baseDir, "state.json")
	if err := os.WriteFile(filename, data, 0644); err != nil {
		return fmt.Errorf("failed to write state file: %w", err)
	}

	return nil
}

// LoadState loads the consensus state.
func (fs *FileStore) LoadState() (*ConsensusState, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	filename := filepath.Join(fs.baseDir, "state.json")
	data, err := os.ReadFile(filename)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // 상태가 없으면 nil 반환
		}
		return nil, fmt.Errorf("failed to read state file: %w", err)
	}

	var state ConsensusState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to unmarshal state: %w", err)
	}

	return &state, nil
}

// ================================================================================
//                          체크포인트 저장/로드
// ================================================================================

// SaveCheckpoint saves a checkpoint.
func (fs *FileStore) SaveCheckpoint(checkpoint *Checkpoint) error {
	if checkpoint == nil {
		return fmt.Errorf("checkpoint is nil")
	}

	fs.mu.Lock()
	defer fs.mu.Unlock()

	data, err := json.MarshalIndent(checkpoint, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal checkpoint: %w", err)
	}

	filename := filepath.Join(fs.baseDir, "checkpoints", fmt.Sprintf("checkpoint_%d.json", checkpoint.SequenceNum))
	if err := os.WriteFile(filename, data, 0644); err != nil {
		return fmt.Errorf("failed to write checkpoint file: %w", err)
	}

	return nil
}

// LoadCheckpoint loads a checkpoint.
func (fs *FileStore) LoadCheckpoint(seqNum uint64) (*Checkpoint, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	filename := filepath.Join(fs.baseDir, "checkpoints", fmt.Sprintf("checkpoint_%d.json", seqNum))
	data, err := os.ReadFile(filename)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read checkpoint file: %w", err)
	}

	var checkpoint Checkpoint
	if err := json.Unmarshal(data, &checkpoint); err != nil {
		return nil, fmt.Errorf("failed to unmarshal checkpoint: %w", err)
	}

	return &checkpoint, nil
}

// LoadCheckpoints loads all checkpoints.
func (fs *FileStore) LoadCheckpoints() ([]*Checkpoint, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	checkpointsDir := filepath.Join(fs.baseDir, "checkpoints")
	entries, err := os.ReadDir(checkpointsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read checkpoints directory: %w", err)
	}

	var checkpoints []*Checkpoint
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		var seqNum uint64
		if _, err := fmt.Sscanf(entry.Name(), "checkpoint_%d.json", &seqNum); err != nil {
			continue
		}

		// 락을 임시로 해제하고 다시 읽기 (재귀 호출 피하기)
		fs.mu.RUnlock()
		cp, err := fs.LoadCheckpoint(seqNum)
		fs.mu.RLock()

		if err != nil {
			return nil, err
		}
		if cp != nil {
			checkpoints = append(checkpoints, cp)
		}
	}

	return checkpoints, nil
}

// GetLatestCheckpoint returns the latest checkpoint.
func (fs *FileStore) GetLatestCheckpoint() (*Checkpoint, error) {
	checkpoints, err := fs.LoadCheckpoints()
	if err != nil {
		return nil, err
	}
	if len(checkpoints) == 0 {
		return nil, nil
	}

	// 가장 높은 시퀀스 번호의 체크포인트 반환
	var latest *Checkpoint
	for _, cp := range checkpoints {
		if latest == nil || cp.SequenceNum > latest.SequenceNum {
			latest = cp
		}
	}
	return latest, nil
}

// Close closes the store.
func (fs *FileStore) Close() error {
	// 파일 기반 저장소는 특별한 종료 로직이 필요 없음
	return nil
}

// ================================================================================
//                          Memory Store (테스트용)
// ================================================================================

// MemoryStore는 메모리 기반 저장소 (테스트용)
type MemoryStore struct {
	mu          sync.RWMutex
	blocks      map[uint64]*types.Block
	state       *ConsensusState
	checkpoints map[uint64]*Checkpoint
}

// NewMemoryStore creates a new memory-based store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		blocks:      make(map[uint64]*types.Block),
		checkpoints: make(map[uint64]*Checkpoint),
	}
}

// SaveBlock saves a block to memory.
func (ms *MemoryStore) SaveBlock(block *types.Block) error {
	if block == nil {
		return fmt.Errorf("block is nil")
	}
	ms.mu.Lock()
	defer ms.mu.Unlock()
	ms.blocks[block.Header.Height] = block
	return nil
}

// LoadBlock loads a block from memory.
func (ms *MemoryStore) LoadBlock(height uint64) (*types.Block, error) {
	ms.mu.RLock()
	defer ms.mu.RUnlock()
	return ms.blocks[height], nil
}

// LoadBlocks loads blocks in a range.
func (ms *MemoryStore) LoadBlocks(fromHeight, toHeight uint64) ([]*types.Block, error) {
	ms.mu.RLock()
	defer ms.mu.RUnlock()
	var blocks []*types.Block
	for h := fromHeight; h <= toHeight; h++ {
		if block, ok := ms.blocks[h]; ok {
			blocks = append(blocks, block)
		}
	}
	return blocks, nil
}

// GetLatestBlockHeight returns the latest block height.
func (ms *MemoryStore) GetLatestBlockHeight() (uint64, error) {
	ms.mu.RLock()
	defer ms.mu.RUnlock()
	var maxHeight uint64
	for h := range ms.blocks {
		if h > maxHeight {
			maxHeight = h
		}
	}
	return maxHeight, nil
}

// SaveState saves the consensus state.
func (ms *MemoryStore) SaveState(state *ConsensusState) error {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	ms.state = state
	return nil
}

// LoadState loads the consensus state.
func (ms *MemoryStore) LoadState() (*ConsensusState, error) {
	ms.mu.RLock()
	defer ms.mu.RUnlock()
	return ms.state, nil
}

// SaveCheckpoint saves a checkpoint.
func (ms *MemoryStore) SaveCheckpoint(checkpoint *Checkpoint) error {
	if checkpoint == nil {
		return fmt.Errorf("checkpoint is nil")
	}
	ms.mu.Lock()
	defer ms.mu.Unlock()
	ms.checkpoints[checkpoint.SequenceNum] = checkpoint
	return nil
}

// LoadCheckpoint loads a checkpoint.
func (ms *MemoryStore) LoadCheckpoint(seqNum uint64) (*Checkpoint, error) {
	ms.mu.RLock()
	defer ms.mu.RUnlock()
	return ms.checkpoints[seqNum], nil
}

// LoadCheckpoints loads all checkpoints.
func (ms *MemoryStore) LoadCheckpoints() ([]*Checkpoint, error) {
	ms.mu.RLock()
	defer ms.mu.RUnlock()
	var checkpoints []*Checkpoint
	for _, cp := range ms.checkpoints {
		checkpoints = append(checkpoints, cp)
	}
	return checkpoints, nil
}

// GetLatestCheckpoint returns the latest checkpoint.
func (ms *MemoryStore) GetLatestCheckpoint() (*Checkpoint, error) {
	ms.mu.RLock()
	defer ms.mu.RUnlock()
	var latest *Checkpoint
	for _, cp := range ms.checkpoints {
		if latest == nil || cp.SequenceNum > latest.SequenceNum {
			latest = cp
		}
	}
	return latest, nil
}

// Close closes the store.
func (ms *MemoryStore) Close() error {
	return nil
}
