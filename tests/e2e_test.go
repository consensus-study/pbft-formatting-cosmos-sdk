// Package tests provides end-to-end integration tests.
package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/ahwlsqja/pbft-cosmos/abci"
	"github.com/ahwlsqja/pbft-cosmos/consensus/pbft"
	"github.com/ahwlsqja/pbft-cosmos/transport"
	"github.com/ahwlsqja/pbft-cosmos/types"
)

// TestE2E_SingleNode_TxToState 단일 노드에서 트랜잭션 → 블록 → 상태 변경 흐름 테스트
func TestE2E_SingleNode_TxToState(t *testing.T) {
	// 1. ABCI Application 생성
	app := abci.NewApplication()

	// 2. LocalABCIAdapter 생성
	adapter := pbft.NewLocalABCIAdapter(app)

	// 3. 검증자 설정 (단일 노드)
	validators := []*types.Validator{
		{ID: "node0", PublicKey: []byte("pubkey0"), Power: 10},
		{ID: "node1", PublicKey: []byte("pubkey1"), Power: 10},
		{ID: "node2", PublicKey: []byte("pubkey2"), Power: 10},
		{ID: "node3", PublicKey: []byte("pubkey3"), Power: 10},
	}
	validatorSet := types.NewValidatorSet(validators)

	// 4. Mock Transport 생성
	mockTransport := newMockTransport("node0")

	// 5. PBFT Engine 생성
	config := &pbft.Config{
		NodeID:             "node0",
		RequestTimeout:     5 * time.Second,
		ViewChangeTimeout:  10 * time.Second,
		CheckpointInterval: 100,
		WindowSize:         200,
	}

	engine, err := pbft.NewEngineV2(config, validatorSet, mockTransport, adapter, nil)
	if err != nil {
		t.Fatalf("Failed to create engine: %v", err)
	}

	// 6. 체인 초기화
	ctx := context.Background()
	if err := engine.InitChain(ctx, "test-chain", nil); err != nil {
		t.Fatalf("InitChain failed: %v", err)
	}

	// 7. 트랜잭션 생성
	op := abci.Operation{Type: "set", Key: "testkey", Value: "testvalue"}
	txData, _ := json.Marshal(op)

	// 8. 블록 생성 및 실행
	block := &types.Block{
		Header: types.BlockHeader{
			Height:     1,
			Timestamp:  time.Now(),
			ProposerID: "node0",
		},
		Transactions: []types.Transaction{
			{ID: "tx1", Data: txData},
		},
		Hash: []byte("blockhash1"),
	}

	// FinalizeBlock 호출
	result, err := adapter.FinalizeBlock(ctx, block)
	if err != nil {
		t.Fatalf("FinalizeBlock failed: %v", err)
	}
	if len(result.AppHash) == 0 {
		t.Fatal("AppHash is empty")
	}

	// Commit 호출
	appHash, _, err := adapter.Commit(ctx)
	if err != nil {
		t.Fatalf("Commit failed: %v", err)
	}
	t.Logf("Committed block 1, appHash: %x", appHash)

	// 9. 상태 검증
	val, err := app.Query("testkey")
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if string(val) != "testvalue" {
		t.Errorf("Expected 'testvalue', got '%s'", val)
	}

	t.Log("E2E Single Node test passed!")
}

// TestE2E_MultiNode_Consensus 4노드 합의 + ABCI 앱 연동 테스트
func TestE2E_MultiNode_Consensus(t *testing.T) {
	const numNodes = 4

	// 노드별 구성 요소
	type nodeSetup struct {
		app       *abci.Application
		adapter   *pbft.LocalABCIAdapter
		engine    *pbft.EngineV2
		transport *transport.GRPCTransport
	}

	nodes := make([]*nodeSetup, numNodes)
	validators := make([]*types.Validator, numNodes)

	// 1. 검증자 목록 생성
	for i := 0; i < numNodes; i++ {
		validators[i] = &types.Validator{
			ID:        fmt.Sprintf("node%d", i),
			PublicKey: []byte(fmt.Sprintf("pubkey%d", i)),
			Power:     10,
		}
	}
	validatorSet := types.NewValidatorSet(validators)

	// 2. 각 노드 설정
	for i := 0; i < numNodes; i++ {
		nodeID := fmt.Sprintf("node%d", i)
		listenAddr := fmt.Sprintf("localhost:%d", 30000+i)

		// ABCI Application
		app := abci.NewApplication()

		// LocalABCIAdapter
		adapter := pbft.NewLocalABCIAdapter(app)

		// gRPC Transport
		trans, err := transport.NewGRPCTransport(nodeID, listenAddr)
		if err != nil {
			t.Fatalf("Failed to create transport for %s: %v", nodeID, err)
		}

		// PBFT Config
		config := &pbft.Config{
			NodeID:             nodeID,
			RequestTimeout:     2 * time.Second,
			ViewChangeTimeout:  5 * time.Second,
			CheckpointInterval: 100,
			WindowSize:         200,
		}

		// Engine 생성
		engine, err := pbft.NewEngineV2(config, validatorSet, trans, adapter, nil)
		if err != nil {
			t.Fatalf("Failed to create engine for %s: %v", nodeID, err)
		}

		nodes[i] = &nodeSetup{
			app:       app,
			adapter:   adapter,
			engine:    engine,
			transport: trans,
		}
	}

	// Cleanup
	defer func() {
		for _, n := range nodes {
			n.engine.Stop()
			n.transport.Stop()
		}
	}()

	// 3. Transport 시작 및 피어 연결
	for _, n := range nodes {
		if err := n.transport.Start(); err != nil {
			t.Fatalf("Failed to start transport: %v", err)
		}
	}

	// 피어 연결
	time.Sleep(100 * time.Millisecond)
	for i, n := range nodes {
		for j := range nodes {
			if i != j {
				peerAddr := fmt.Sprintf("localhost:%d", 30000+j)
				peerID := fmt.Sprintf("node%d", j)
				if err := n.transport.AddPeer(peerID, peerAddr); err != nil {
					t.Logf("Warning: failed to connect node%d to %s: %v", i, peerID, err)
				}
			}
		}
	}

	// 4. 엔진 시작
	for _, n := range nodes {
		if err := n.engine.Start(); err != nil {
			t.Fatalf("Failed to start engine: %v", err)
		}
	}

	time.Sleep(500 * time.Millisecond)

	// 5. 트랜잭션 제출 (리더 노드)
	op := abci.Operation{Type: "set", Key: "consensus-key", Value: "consensus-value"}
	txData, _ := json.Marshal(op)

	// node0이 리더 (view 0)
	if err := nodes[0].engine.SubmitRequest(txData, "test-client"); err != nil {
		t.Fatalf("SubmitRequest failed: %v", err)
	}

	// 6. 합의 완료 대기
	deadline := time.Now().Add(10 * time.Second)
	var allCommitted bool

	for time.Now().Before(deadline) {
		allCommitted = true
		for i, n := range nodes {
			height := n.engine.GetCurrentHeight()
			if height < 1 {
				allCommitted = false
				t.Logf("Node %d height: %d", i, height)
			}
		}
		if allCommitted {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	if !allCommitted {
		t.Fatal("Not all nodes committed the block within timeout")
	}

	// 7. 모든 노드의 상태 일관성 검증
	t.Log("Verifying state consistency across all nodes...")
	for i, n := range nodes {
		val, err := n.app.Query("consensus-key")
		if err != nil {
			t.Errorf("Node %d: Query failed: %v", i, err)
			continue
		}
		if string(val) != "consensus-value" {
			t.Errorf("Node %d: Expected 'consensus-value', got '%s'", i, val)
		}
		t.Logf("Node %d: consensus-key = %s, height = %d",
			i, string(val), n.engine.GetCurrentHeight())
	}

	t.Log("E2E Multi-Node Consensus test passed!")
}

// TestE2E_MultipleBlocks 다중 블록 연속 처리 테스트
func TestE2E_MultipleBlocks(t *testing.T) {
	const numNodes = 4
	const numBlocks = 3

	// 노드 설정
	type nodeSetup struct {
		app       *abci.Application
		adapter   *pbft.LocalABCIAdapter
		engine    *pbft.EngineV2
		transport *transport.GRPCTransport
	}

	nodes := make([]*nodeSetup, numNodes)
	validators := make([]*types.Validator, numNodes)

	for i := 0; i < numNodes; i++ {
		validators[i] = &types.Validator{
			ID:        fmt.Sprintf("node%d", i),
			PublicKey: []byte(fmt.Sprintf("pubkey%d", i)),
			Power:     10,
		}
	}
	validatorSet := types.NewValidatorSet(validators)

	for i := 0; i < numNodes; i++ {
		nodeID := fmt.Sprintf("node%d", i)
		listenAddr := fmt.Sprintf("localhost:%d", 31000+i)

		app := abci.NewApplication()
		adapter := pbft.NewLocalABCIAdapter(app)
		trans, err := transport.NewGRPCTransport(nodeID, listenAddr)
		if err != nil {
			t.Fatalf("Failed to create transport: %v", err)
		}

		config := &pbft.Config{
			NodeID:             nodeID,
			RequestTimeout:     2 * time.Second,
			ViewChangeTimeout:  5 * time.Second,
			CheckpointInterval: 100,
			WindowSize:         200,
		}

		engine, err := pbft.NewEngineV2(config, validatorSet, trans, adapter, nil)
		if err != nil {
			t.Fatalf("Failed to create engine: %v", err)
		}

		nodes[i] = &nodeSetup{
			app:       app,
			adapter:   adapter,
			engine:    engine,
			transport: trans,
		}
	}

	defer func() {
		for _, n := range nodes {
			n.engine.Stop()
			n.transport.Stop()
		}
	}()

	// Transport 시작
	for _, n := range nodes {
		if err := n.transport.Start(); err != nil {
			t.Fatalf("Failed to start transport: %v", err)
		}
	}

	time.Sleep(100 * time.Millisecond)

	// 피어 연결
	for i, n := range nodes {
		for j := range nodes {
			if i != j {
				peerAddr := fmt.Sprintf("localhost:%d", 31000+j)
				peerID := fmt.Sprintf("node%d", j)
				n.transport.AddPeer(peerID, peerAddr)
			}
		}
	}

	// 엔진 시작
	for _, n := range nodes {
		if err := n.engine.Start(); err != nil {
			t.Fatalf("Failed to start engine: %v", err)
		}
	}

	time.Sleep(500 * time.Millisecond)

	// 여러 블록 제출
	for blockNum := 1; blockNum <= numBlocks; blockNum++ {
		op := abci.Operation{
			Type:  "set",
			Key:   fmt.Sprintf("block%d-key", blockNum),
			Value: fmt.Sprintf("block%d-value", blockNum),
		}
		txData, _ := json.Marshal(op)

		if err := nodes[0].engine.SubmitRequest(txData, "test-client"); err != nil {
			t.Fatalf("Block %d: SubmitRequest failed: %v", blockNum, err)
		}

		// 모든 노드가 이 블록을 커밋할 때까지 대기
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			allCommitted := true
			for _, n := range nodes {
				if n.engine.GetCurrentHeight() < uint64(blockNum) {
					allCommitted = false
					break
				}
			}
			if allCommitted {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}

		t.Logf("Block %d committed on all nodes, leader height: %d", blockNum, nodes[0].engine.GetCurrentHeight())
	}

	// 최종 높이 확인 (여유 시간 추가)
	time.Sleep(500 * time.Millisecond)
	for i, n := range nodes {
		height := n.engine.GetCurrentHeight()
		t.Logf("Node %d final height: %d", i, height)
		if height < uint64(numBlocks) {
			t.Errorf("Node %d: Expected height >= %d, got %d", i, numBlocks, height)
		}
	}

	// 모든 상태 검증
	for blockNum := 1; blockNum <= numBlocks; blockNum++ {
		key := fmt.Sprintf("block%d-key", blockNum)
		expectedValue := fmt.Sprintf("block%d-value", blockNum)

		for i, n := range nodes {
			val, err := n.app.Query(key)
			if err != nil {
				t.Errorf("Node %d: Query %s failed: %v", i, key, err)
				continue
			}
			if string(val) != expectedValue {
				t.Errorf("Node %d: %s expected '%s', got '%s'", i, key, expectedValue, val)
			}
		}
	}

	t.Log("E2E Multiple Blocks test passed!")
}

// TestE2E_StateConsistency 상태 일관성 상세 검증
func TestE2E_StateConsistency(t *testing.T) {
	app := abci.NewApplication()
	adapter := pbft.NewLocalABCIAdapter(app)
	ctx := context.Background()

	// InitChain
	validators := []*types.Validator{
		{ID: "node0", Power: 10},
	}
	if err := adapter.InitChain(ctx, "test-chain", validators, nil); err != nil {
		t.Fatalf("InitChain failed: %v", err)
	}

	// 블록 1: 여러 키 설정
	block1 := &types.Block{
		Header: types.BlockHeader{
			Height:     1,
			Timestamp:  time.Now(),
			ProposerID: "node0",
		},
		Transactions: []types.Transaction{
			{ID: "tx1", Data: mustMarshalOp(abci.Operation{Type: "set", Key: "a", Value: "1"})},
			{ID: "tx2", Data: mustMarshalOp(abci.Operation{Type: "set", Key: "b", Value: "2"})},
			{ID: "tx3", Data: mustMarshalOp(abci.Operation{Type: "set", Key: "c", Value: "3"})},
		},
		Hash: []byte("hash1"),
	}

	_, err := adapter.FinalizeBlock(ctx, block1)
	if err != nil {
		t.Fatalf("FinalizeBlock 1 failed: %v", err)
	}
	if _, _, err := adapter.Commit(ctx); err != nil {
		t.Fatalf("Commit 1 failed: %v", err)
	}

	// 중간 상태 검증
	for _, kv := range []struct{ k, v string }{{"a", "1"}, {"b", "2"}, {"c", "3"}} {
		val, err := app.Query(kv.k)
		if err != nil {
			t.Errorf("Query %s failed: %v", kv.k, err)
		} else if string(val) != kv.v {
			t.Errorf("Key %s: expected %s, got %s", kv.k, kv.v, val)
		}
	}

	// 블록 2: 업데이트 및 삭제
	block2 := &types.Block{
		Header: types.BlockHeader{
			Height:     2,
			Timestamp:  time.Now(),
			ProposerID: "node0",
		},
		Transactions: []types.Transaction{
			{ID: "tx4", Data: mustMarshalOp(abci.Operation{Type: "set", Key: "a", Value: "10"})}, // 업데이트
			{ID: "tx5", Data: mustMarshalOp(abci.Operation{Type: "delete", Key: "b"})},          // 삭제
			{ID: "tx6", Data: mustMarshalOp(abci.Operation{Type: "set", Key: "d", Value: "4"})}, // 추가
		},
		Hash: []byte("hash2"),
	}

	_, err = adapter.FinalizeBlock(ctx, block2)
	if err != nil {
		t.Fatalf("FinalizeBlock 2 failed: %v", err)
	}
	if _, _, err := adapter.Commit(ctx); err != nil {
		t.Fatalf("Commit 2 failed: %v", err)
	}

	// 최종 상태 검증
	// a: 10 (업데이트됨)
	val, err := app.Query("a")
	if err != nil {
		t.Fatalf("Query a failed: %v", err)
	}
	if string(val) != "10" {
		t.Errorf("Key a: expected 10, got %s", val)
	}

	// b: 삭제됨
	_, err = app.Query("b")
	if err == nil {
		t.Error("Key b should be deleted")
	}

	// c: 3 (변경 없음)
	val, err = app.Query("c")
	if err != nil {
		t.Fatalf("Query c failed: %v", err)
	}
	if string(val) != "3" {
		t.Errorf("Key c: expected 3, got %s", val)
	}

	// d: 4 (추가됨)
	val, err = app.Query("d")
	if err != nil {
		t.Fatalf("Query d failed: %v", err)
	}
	if string(val) != "4" {
		t.Errorf("Key d: expected 4, got %s", val)
	}

	// Height 확인
	if app.GetHeight() != 2 {
		t.Errorf("Expected height 2, got %d", app.GetHeight())
	}

	t.Log("E2E State Consistency test passed!")
}

// mockTransport는 테스트용 mock transport
type mockTransport struct {
	nodeID  string
	handler func(*pbft.Message)
	mu      sync.Mutex
}

func newMockTransport(nodeID string) *mockTransport {
	return &mockTransport{nodeID: nodeID}
}

func (m *mockTransport) Broadcast(msg *pbft.Message) error {
	return nil
}

func (m *mockTransport) Send(nodeID string, msg *pbft.Message) error {
	return nil
}

func (m *mockTransport) SetMessageHandler(handler func(*pbft.Message)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.handler = handler
}

func mustMarshalOp(op abci.Operation) []byte {
	data, err := json.Marshal(op)
	if err != nil {
		panic(err)
	}
	return data
}
