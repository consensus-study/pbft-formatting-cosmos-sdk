// Package abci provides ABCI application tests.
package abci

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/ahwlsqja/pbft-cosmos/types"
)

func TestApplication_ExecuteBlock(t *testing.T) {
	app := NewApplication()

	// 블록 생성
	block := &types.Block{
		Header: types.BlockHeader{
			Height:    1,
			Timestamp: time.Now(),
		},
		Transactions: []types.Transaction{
			{
				ID:   "tx1",
				Data: mustMarshalOp(Operation{Type: "set", Key: "key1", Value: "value1"}),
			},
			{
				ID:   "tx2",
				Data: mustMarshalOp(Operation{Type: "set", Key: "key2", Value: "value2"}),
			},
		},
		Hash: []byte("blockhash1"),
	}

	// 블록 실행
	appHash, err := app.ExecuteBlock(block)
	if err != nil {
		t.Fatalf("ExecuteBlock failed: %v", err)
	}
	if len(appHash) == 0 {
		t.Fatal("AppHash is empty")
	}

	// 상태 커밋
	if err := app.Commit(block); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// 쿼리 테스트
	val1, err := app.Query("key1")
	if err != nil {
		t.Fatalf("Query key1 failed: %v", err)
	}
	if string(val1) != "value1" {
		t.Errorf("Expected value1, got %s", val1)
	}

	val2, err := app.Query("key2")
	if err != nil {
		t.Fatalf("Query key2 failed: %v", err)
	}
	if string(val2) != "value2" {
		t.Errorf("Expected value2, got %s", val2)
	}

	// Height 확인
	if app.GetHeight() != 1 {
		t.Errorf("Expected height 1, got %d", app.GetHeight())
	}
}

func TestApplication_ValidateBlock(t *testing.T) {
	app := NewApplication()

	// 유효한 블록
	validBlock := &types.Block{
		Header: types.BlockHeader{Height: 1},
		Transactions: []types.Transaction{
			{ID: "tx1", Data: []byte("data")},
		},
	}
	if err := app.ValidateBlock(validBlock); err != nil {
		t.Errorf("Expected valid block, got error: %v", err)
	}

	// nil 블록
	if err := app.ValidateBlock(nil); err == nil {
		t.Error("Expected error for nil block")
	}

	// 빈 트랜잭션 ID
	invalidTx := &types.Block{
		Header: types.BlockHeader{Height: 1},
		Transactions: []types.Transaction{
			{ID: "", Data: []byte("data")},
		},
	}
	if err := app.ValidateBlock(invalidTx); err == nil {
		t.Error("Expected error for empty tx ID")
	}
}

func TestApplication_DeleteOperation(t *testing.T) {
	app := NewApplication()

	// 먼저 값 설정
	block1 := &types.Block{
		Header: types.BlockHeader{Height: 1},
		Transactions: []types.Transaction{
			{
				ID:   "tx1",
				Data: mustMarshalOp(Operation{Type: "set", Key: "mykey", Value: "myvalue"}),
			},
		},
	}
	if _, err := app.ExecuteBlock(block1); err != nil {
		t.Fatalf("ExecuteBlock failed: %v", err)
	}
	if err := app.Commit(block1); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// 값이 존재하는지 확인
	val, err := app.Query("mykey")
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if string(val) != "myvalue" {
		t.Errorf("Expected myvalue, got %s", val)
	}

	// 삭제 트랜잭션 실행
	block2 := &types.Block{
		Header: types.BlockHeader{Height: 2},
		Transactions: []types.Transaction{
			{
				ID:   "tx2",
				Data: mustMarshalOp(Operation{Type: "delete", Key: "mykey"}),
			},
		},
	}
	if _, err := app.ExecuteBlock(block2); err != nil {
		t.Fatalf("ExecuteBlock failed: %v", err)
	}
	if err := app.Commit(block2); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// 삭제 확인
	_, err = app.Query("mykey")
	if err == nil {
		t.Error("Expected error for deleted key")
	}
}

func TestApplication_PendingTransactions(t *testing.T) {
	app := NewApplication()

	// 대기 트랜잭션 추가
	tx1 := types.Transaction{ID: "tx1", Data: []byte("data1")}
	tx2 := types.Transaction{ID: "tx2", Data: []byte("data2")}

	if err := app.AddPendingTransaction(tx1); err != nil {
		t.Fatalf("AddPendingTransaction failed: %v", err)
	}
	if err := app.AddPendingTransaction(tx2); err != nil {
		t.Fatalf("AddPendingTransaction failed: %v", err)
	}

	// 대기 트랜잭션 가져오기
	pending := app.GetPendingTransactions()
	if len(pending) != 2 {
		t.Errorf("Expected 2 pending txs, got %d", len(pending))
	}

	// 가져온 후 비워짐
	pending2 := app.GetPendingTransactions()
	if len(pending2) != 0 {
		t.Errorf("Expected 0 pending txs after get, got %d", len(pending2))
	}
}

func TestApplication_ABCIMethods(t *testing.T) {
	app := NewApplication()

	// InitChain
	validators := []*types.Validator{
		{ID: "node0", Power: 10},
		{ID: "node1", Power: 10},
	}
	if err := app.InitChain(validators); err != nil {
		t.Fatalf("InitChain failed: %v", err)
	}

	// Info
	info := app.GetInfo()
	if info == nil {
		t.Fatal("GetInfo returned nil")
	}
	if info.Version != "1.0.0" {
		t.Errorf("Expected version 1.0.0, got %s", info.Version)
	}

	// BeginBlock
	if err := app.BeginBlock(1, "proposer1"); err != nil {
		t.Errorf("BeginBlock failed: %v", err)
	}

	// EndBlock
	resp, err := app.EndBlock(1)
	if err != nil {
		t.Fatalf("EndBlock failed: %v", err)
	}
	if resp == nil {
		t.Fatal("EndBlock response is nil")
	}

	// CheckTx
	tx := types.Transaction{ID: "check-tx", Data: []byte("data")}
	if err := app.CheckTx(tx); err != nil {
		t.Errorf("CheckTx failed: %v", err)
	}

	// DeliverTx
	resp2, err := app.DeliverTx(types.Transaction{
		ID:   "deliver-tx",
		Data: mustMarshalOp(Operation{Type: "set", Key: "key", Value: "val"}),
	})
	if err != nil {
		t.Fatalf("DeliverTx failed: %v", err)
	}
	if resp2.Code != 0 {
		t.Errorf("Expected code 0, got %d", resp2.Code)
	}
}

func TestApplication_Snapshot(t *testing.T) {
	app := NewApplication()

	// 상태 설정
	block := &types.Block{
		Header: types.BlockHeader{Height: 1},
		Transactions: []types.Transaction{
			{
				ID:   "tx1",
				Data: mustMarshalOp(Operation{Type: "set", Key: "snapshot-key", Value: "snapshot-value"}),
			},
		},
	}
	if _, err := app.ExecuteBlock(block); err != nil {
		t.Fatalf("ExecuteBlock failed: %v", err)
	}
	if err := app.Commit(block); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// 스냅샷 생성
	snapshot, err := app.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot failed: %v", err)
	}
	if len(snapshot) == 0 {
		t.Fatal("Snapshot is empty")
	}

	// 새 앱에 스냅샷 복원
	app2 := NewApplication()
	if err := app2.RestoreSnapshot(snapshot); err != nil {
		t.Fatalf("RestoreSnapshot failed: %v", err)
	}

	// 복원된 상태 확인
	val, err := app2.Query("snapshot-key")
	if err != nil {
		t.Fatalf("Query after restore failed: %v", err)
	}
	if string(val) != "snapshot-value" {
		t.Errorf("Expected snapshot-value, got %s", val)
	}
}

func TestApplication_MultiBlockExecution(t *testing.T) {
	app := NewApplication()

	// 블록 1: key1, key2 설정
	block1 := &types.Block{
		Header: types.BlockHeader{Height: 1, Timestamp: time.Now()},
		Transactions: []types.Transaction{
			{ID: "tx1", Data: mustMarshalOp(Operation{Type: "set", Key: "key1", Value: "v1"})},
			{ID: "tx2", Data: mustMarshalOp(Operation{Type: "set", Key: "key2", Value: "v2"})},
		},
		Hash: []byte("hash1"),
	}
	if _, err := app.ExecuteBlock(block1); err != nil {
		t.Fatalf("Block1 ExecuteBlock failed: %v", err)
	}
	if err := app.Commit(block1); err != nil {
		t.Fatalf("Block1 Commit failed: %v", err)
	}

	// 블록 2: key1 업데이트, key3 추가
	block2 := &types.Block{
		Header: types.BlockHeader{Height: 2, Timestamp: time.Now()},
		Transactions: []types.Transaction{
			{ID: "tx3", Data: mustMarshalOp(Operation{Type: "set", Key: "key1", Value: "v1-updated"})},
			{ID: "tx4", Data: mustMarshalOp(Operation{Type: "set", Key: "key3", Value: "v3"})},
		},
		Hash: []byte("hash2"),
	}
	if _, err := app.ExecuteBlock(block2); err != nil {
		t.Fatalf("Block2 ExecuteBlock failed: %v", err)
	}
	if err := app.Commit(block2); err != nil {
		t.Fatalf("Block2 Commit failed: %v", err)
	}

	// 블록 3: key2 삭제
	block3 := &types.Block{
		Header: types.BlockHeader{Height: 3, Timestamp: time.Now()},
		Transactions: []types.Transaction{
			{ID: "tx5", Data: mustMarshalOp(Operation{Type: "delete", Key: "key2"})},
		},
		Hash: []byte("hash3"),
	}
	if _, err := app.ExecuteBlock(block3); err != nil {
		t.Fatalf("Block3 ExecuteBlock failed: %v", err)
	}
	if err := app.Commit(block3); err != nil {
		t.Fatalf("Block3 Commit failed: %v", err)
	}

	// 최종 상태 검증
	if app.GetHeight() != 3 {
		t.Errorf("Expected height 3, got %d", app.GetHeight())
	}

	val1, err := app.Query("key1")
	if err != nil {
		t.Fatalf("Query key1 failed: %v", err)
	}
	if string(val1) != "v1-updated" {
		t.Errorf("Expected v1-updated, got %s", val1)
	}

	_, err = app.Query("key2")
	if err == nil {
		t.Error("Expected error for deleted key2")
	}

	val3, err := app.Query("key3")
	if err != nil {
		t.Fatalf("Query key3 failed: %v", err)
	}
	if string(val3) != "v3" {
		t.Errorf("Expected v3, got %s", val3)
	}

	// 블록 히스토리 확인
	history := app.GetBlockHistory()
	if len(history) != 3 {
		t.Errorf("Expected 3 blocks in history, got %d", len(history))
	}
}

func TestApplication_RawDataFallback(t *testing.T) {
	app := NewApplication()

	// JSON이 아닌 원시 데이터 트랜잭션
	block := &types.Block{
		Header: types.BlockHeader{Height: 1},
		Transactions: []types.Transaction{
			{ID: "raw-tx", Data: []byte("raw data without json")},
		},
	}
	if _, err := app.ExecuteBlock(block); err != nil {
		t.Fatalf("ExecuteBlock failed: %v", err)
	}
	if err := app.Commit(block); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// 원시 데이터가 tx ID를 키로 저장되는지 확인
	val, err := app.Query("raw-tx")
	if err != nil {
		t.Fatalf("Query raw-tx failed: %v", err)
	}
	if string(val) != "raw data without json" {
		t.Errorf("Expected raw data, got %s", val)
	}
}

func mustMarshalOp(op Operation) []byte {
	data, err := json.Marshal(op)
	if err != nil {
		panic(err)
	}
	return data
}
