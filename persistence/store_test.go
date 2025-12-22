package persistence

import (
	"os"
	"testing"
	"time"

	"github.com/ahwlsqja/pbft-cosmos/types"
)

func TestFileStore(t *testing.T) {
	// 임시 디렉토리 생성
	tmpDir, err := os.MkdirTemp("", "persistence_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// FileStore 생성
	store, err := NewFileStore(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create file store: %v", err)
	}
	defer store.Close()

	// 블록 테스트
	t.Run("SaveAndLoadBlock", func(t *testing.T) {
		block := &types.Block{
			Header: types.BlockHeader{
				Height:    1,
				PrevHash:  []byte("prevhash"),
				Timestamp: time.Now(),
			},
			Transactions: []types.Transaction{
				{ID: "tx1", Data: []byte("data1")},
			},
			Hash: []byte("blockhash1"),
		}

		// 저장
		if err := store.SaveBlock(block); err != nil {
			t.Fatalf("Failed to save block: %v", err)
		}

		// 로드
		loaded, err := store.LoadBlock(1)
		if err != nil {
			t.Fatalf("Failed to load block: %v", err)
		}
		if loaded == nil {
			t.Fatal("Loaded block is nil")
		}
		if loaded.Header.Height != 1 {
			t.Errorf("Expected height 1, got %d", loaded.Header.Height)
		}
		if len(loaded.Transactions) != 1 {
			t.Errorf("Expected 1 tx, got %d", len(loaded.Transactions))
		}
	})

	// 상태 테스트
	t.Run("SaveAndLoadState", func(t *testing.T) {
		state := &ConsensusState{
			NodeID:        "node-1",
			CurrentView:   5,
			CurrentHeight: 100,
			LastAppHash:   []byte("apphash"),
		}

		// 저장
		if err := store.SaveState(state); err != nil {
			t.Fatalf("Failed to save state: %v", err)
		}

		// 로드
		loaded, err := store.LoadState()
		if err != nil {
			t.Fatalf("Failed to load state: %v", err)
		}
		if loaded == nil {
			t.Fatal("Loaded state is nil")
		}
		if loaded.NodeID != "node-1" {
			t.Errorf("Expected node-1, got %s", loaded.NodeID)
		}
		if loaded.CurrentView != 5 {
			t.Errorf("Expected view 5, got %d", loaded.CurrentView)
		}
		if loaded.CurrentHeight != 100 {
			t.Errorf("Expected height 100, got %d", loaded.CurrentHeight)
		}
	})

	// 체크포인트 테스트
	t.Run("SaveAndLoadCheckpoint", func(t *testing.T) {
		cp := &Checkpoint{
			SequenceNum: 50,
			Digest:      []byte("digest50"),
			NodeID:      "node-1",
		}

		// 저장
		if err := store.SaveCheckpoint(cp); err != nil {
			t.Fatalf("Failed to save checkpoint: %v", err)
		}

		// 로드
		loaded, err := store.LoadCheckpoint(50)
		if err != nil {
			t.Fatalf("Failed to load checkpoint: %v", err)
		}
		if loaded == nil {
			t.Fatal("Loaded checkpoint is nil")
		}
		if loaded.SequenceNum != 50 {
			t.Errorf("Expected seq 50, got %d", loaded.SequenceNum)
		}
	})

	// 여러 블록 테스트
	t.Run("LoadBlocks", func(t *testing.T) {
		// 블록 2, 3 추가
		for i := uint64(2); i <= 3; i++ {
			block := &types.Block{
				Header: types.BlockHeader{Height: i},
				Hash:   []byte{byte(i)},
			}
			if err := store.SaveBlock(block); err != nil {
				t.Fatalf("Failed to save block %d: %v", i, err)
			}
		}

		// 범위 로드
		blocks, err := store.LoadBlocks(1, 3)
		if err != nil {
			t.Fatalf("Failed to load blocks: %v", err)
		}
		if len(blocks) != 3 {
			t.Errorf("Expected 3 blocks, got %d", len(blocks))
		}

		// 최신 높이 확인
		height, err := store.GetLatestBlockHeight()
		if err != nil {
			t.Fatalf("Failed to get latest height: %v", err)
		}
		if height != 3 {
			t.Errorf("Expected height 3, got %d", height)
		}
	})

	// 최신 체크포인트 테스트
	t.Run("GetLatestCheckpoint", func(t *testing.T) {
		// 체크포인트 100 추가
		cp := &Checkpoint{
			SequenceNum: 100,
			Digest:      []byte("digest100"),
			NodeID:      "node-1",
		}
		if err := store.SaveCheckpoint(cp); err != nil {
			t.Fatalf("Failed to save checkpoint: %v", err)
		}

		latest, err := store.GetLatestCheckpoint()
		if err != nil {
			t.Fatalf("Failed to get latest checkpoint: %v", err)
		}
		if latest == nil {
			t.Fatal("Latest checkpoint is nil")
		}
		if latest.SequenceNum != 100 {
			t.Errorf("Expected seq 100, got %d", latest.SequenceNum)
		}
	})
}

func TestMemoryStore(t *testing.T) {
	store := NewMemoryStore()

	// 블록 저장/로드
	block := &types.Block{
		Header: types.BlockHeader{Height: 1},
		Hash:   []byte("hash1"),
	}

	if err := store.SaveBlock(block); err != nil {
		t.Fatalf("Failed to save block: %v", err)
	}

	loaded, err := store.LoadBlock(1)
	if err != nil {
		t.Fatalf("Failed to load block: %v", err)
	}
	if loaded == nil {
		t.Fatal("Loaded block is nil")
	}

	// 상태 저장/로드
	state := &ConsensusState{NodeID: "test-node"}
	if err := store.SaveState(state); err != nil {
		t.Fatalf("Failed to save state: %v", err)
	}

	loadedState, err := store.LoadState()
	if err != nil {
		t.Fatalf("Failed to load state: %v", err)
	}
	if loadedState.NodeID != "test-node" {
		t.Errorf("Expected test-node, got %s", loadedState.NodeID)
	}

	// 체크포인트 저장/로드
	cp := &Checkpoint{SequenceNum: 10, Digest: []byte("digest")}
	if err := store.SaveCheckpoint(cp); err != nil {
		t.Fatalf("Failed to save checkpoint: %v", err)
	}

	loadedCP, err := store.LoadCheckpoint(10)
	if err != nil {
		t.Fatalf("Failed to load checkpoint: %v", err)
	}
	if loadedCP.SequenceNum != 10 {
		t.Errorf("Expected seq 10, got %d", loadedCP.SequenceNum)
	}
}
