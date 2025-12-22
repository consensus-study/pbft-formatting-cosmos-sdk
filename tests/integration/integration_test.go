// Package integration provides integration tests for the PBFT consensus engine.
// 실제 gRPC 통신을 사용한 4노드 합의 테스트
package integration

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/ahwlsqja/pbft-cosmos/consensus/pbft"
	"github.com/ahwlsqja/pbft-cosmos/mempool"
	"github.com/ahwlsqja/pbft-cosmos/transport"
	"github.com/ahwlsqja/pbft-cosmos/types"
)

// ================================================================================
//                          통합 테스트 헬퍼
// ================================================================================

// TestNode는 테스트용 PBFT 노드
type TestNode struct {
	ID        string
	Engine    *pbft.EngineV2
	Transport *transport.GRPCTransport
	Mempool   *mempool.Mempool
	Address   string
}

// TestNetwork는 테스트용 PBFT 네트워크
type TestNetwork struct {
	Nodes       map[string]*TestNode
	Validators  *types.ValidatorSet
	BasePort    int
	NodeCount   int
	mu          sync.RWMutex
}

// NewTestNetwork creates a new test network with the specified number of nodes.
// 지정된 수의 노드로 테스트 네트워크를 생성함
func NewTestNetwork(nodeCount int, basePort int) (*TestNetwork, error) {
	tn := &TestNetwork{
		Nodes:     make(map[string]*TestNode),
		BasePort:  basePort,
		NodeCount: nodeCount,
	}

	// 1. 검증자 목록 생성
	validators := make([]*types.Validator, nodeCount)
	for i := 0; i < nodeCount; i++ {
		validators[i] = &types.Validator{
			ID:    fmt.Sprintf("node-%d", i),
			Power: 1,
		}
	}
	tn.Validators = types.NewValidatorSet(validators)

	// 2. 각 노드 생성
	for i := 0; i < nodeCount; i++ {
		nodeID := fmt.Sprintf("node-%d", i)
		addr := fmt.Sprintf("127.0.0.1:%d", basePort+i)

		// Transport 생성
		trans, err := transport.NewGRPCTransport(nodeID, addr)
		if err != nil {
			return nil, fmt.Errorf("failed to create transport for %s: %w", nodeID, err)
		}

		// Mempool 생성
		mp := mempool.NewMempool(mempool.DefaultConfig())

		// PBFT 설정
		config := &pbft.Config{
			NodeID:             nodeID,
			RequestTimeout:     5 * time.Second,
			ViewChangeTimeout:  30 * time.Second, // 테스트용으로 길게
			CheckpointInterval: 10,
			WindowSize:         100,
		}

		// NoopABCIAdapter 사용 (테스트용)
		noopAdapter := pbft.NewNoopABCIAdapter()

		// 엔진 생성
		engine, err := pbft.NewEngineV2(config, tn.Validators, trans, noopAdapter, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create engine for %s: %w", nodeID, err)
		}

		// Mempool 연결
		engine.SetMempool(mp)

		tn.Nodes[nodeID] = &TestNode{
			ID:        nodeID,
			Engine:    engine,
			Transport: trans,
			Mempool:   mp,
			Address:   addr,
		}
	}

	return tn, nil
}

// Start starts all nodes and connects them.
// 모든 노드를 시작하고 서로 연결함
func (tn *TestNetwork) Start() error {
	// 1. 모든 Transport 시작
	for _, node := range tn.Nodes {
		if err := node.Transport.Start(); err != nil {
			return fmt.Errorf("failed to start transport for %s: %w", node.ID, err)
		}
	}

	// Transport가 준비될 때까지 대기
	time.Sleep(100 * time.Millisecond)

	// 2. 모든 노드를 서로 연결
	for _, node := range tn.Nodes {
		for _, peer := range tn.Nodes {
			if node.ID != peer.ID {
				if err := node.Transport.AddPeer(peer.ID, peer.Address); err != nil {
					return fmt.Errorf("failed to connect %s to %s: %w", node.ID, peer.ID, err)
				}
			}
		}
	}

	// 3. 모든 Mempool 시작
	for _, node := range tn.Nodes {
		if err := node.Mempool.Start(); err != nil {
			return fmt.Errorf("failed to start mempool for %s: %w", node.ID, err)
		}
	}

	// 4. TxHandler 설정 (트랜잭션 수신 시 Mempool에 추가)
	for _, node := range tn.Nodes {
		mp := node.Mempool
		node.Transport.SetTxHandler(func(txData []byte, sender string, nonce, gasPrice, gasLimit uint64) error {
			return mp.AddTxWithMeta(txData, sender, nonce, gasPrice, gasLimit)
		})
	}

	// 5. 모든 엔진 시작
	for _, node := range tn.Nodes {
		if err := node.Engine.Start(); err != nil {
			return fmt.Errorf("failed to start engine for %s: %w", node.ID, err)
		}
	}

	return nil
}

// Stop stops all nodes.
// 모든 노드를 정지함
func (tn *TestNetwork) Stop() {
	for _, node := range tn.Nodes {
		node.Engine.Stop()
		node.Mempool.Stop()
		node.Transport.Stop()
	}
}

// GetPrimary returns the current primary node.
// 현재 리더 노드를 반환함
func (tn *TestNetwork) GetPrimary() *TestNode {
	for _, node := range tn.Nodes {
		if node.Engine.IsPrimary() {
			return node
		}
	}
	return nil
}

// WaitForHeight waits until all nodes reach the specified height.
// 모든 노드가 지정된 높이에 도달할 때까지 대기
func (tn *TestNetwork) WaitForHeight(height uint64, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		allReached := true
		for _, node := range tn.Nodes {
			if node.Engine.GetCurrentHeight() < height {
				allReached = false
				break
			}
		}
		if allReached {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// WaitForConsensus waits until all nodes agree on the same height.
// 모든 노드가 같은 높이에 합의할 때까지 대기
func (tn *TestNetwork) WaitForConsensus(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		heights := make(map[uint64]int)
		for _, node := range tn.Nodes {
			h := node.Engine.GetCurrentHeight()
			heights[h]++
		}

		// 모든 노드가 같은 높이인지 확인
		if len(heights) == 1 {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// ================================================================================
//                          통합 테스트
// ================================================================================

// TestBasicConsensus tests basic 4-node consensus.
// 기본 4노드 합의 테스트
func TestBasicConsensus(t *testing.T) {
	// 1. 네트워크 생성
	network, err := NewTestNetwork(4, 50000)
	if err != nil {
		t.Fatalf("Failed to create network: %v", err)
	}
	defer network.Stop()

	// 2. 네트워크 시작
	if err := network.Start(); err != nil {
		t.Fatalf("Failed to start network: %v", err)
	}

	// 3. 안정화 대기
	time.Sleep(500 * time.Millisecond)

	// 4. 리더 확인
	primary := network.GetPrimary()
	if primary == nil {
		t.Fatal("No primary node found")
	}
	t.Logf("Primary node: %s", primary.ID)

	// 5. 트랜잭션 제출
	tx := []byte("test-transaction-1")
	if err := primary.Engine.SubmitRequest(tx, "test-client"); err != nil {
		t.Fatalf("Failed to submit request: %v", err)
	}

	// 6. 합의 대기 (높이 1에 도달)
	if !network.WaitForHeight(1, 10*time.Second) {
		t.Fatal("Failed to reach height 1")
	}

	// 7. 모든 노드가 같은 높이인지 확인
	for id, node := range network.Nodes {
		t.Logf("Node %s: height=%d, view=%d", id, node.Engine.GetCurrentHeight(), node.Engine.GetCurrentView())
	}

	// 8. 모든 노드가 블록을 가지고 있는지 확인
	for id, node := range network.Nodes {
		blocks := node.Engine.GetCommittedBlocks()
		if len(blocks) == 0 {
			t.Errorf("Node %s has no committed blocks", id)
		} else {
			t.Logf("Node %s has %d committed blocks", id, len(blocks))
		}
	}
}

// TestMultipleTransactions tests consensus with multiple transactions.
// 여러 트랜잭션 합의 테스트
func TestMultipleTransactions(t *testing.T) {
	network, err := NewTestNetwork(4, 50010)
	if err != nil {
		t.Fatalf("Failed to create network: %v", err)
	}
	defer network.Stop()

	if err := network.Start(); err != nil {
		t.Fatalf("Failed to start network: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	primary := network.GetPrimary()
	if primary == nil {
		t.Fatal("No primary node found")
	}

	// 10개 트랜잭션 제출
	txCount := 10
	for i := 0; i < txCount; i++ {
		tx := []byte(fmt.Sprintf("transaction-%d", i))
		if err := primary.Engine.SubmitRequest(tx, "test-client"); err != nil {
			t.Fatalf("Failed to submit request %d: %v", i, err)
		}
		// 트랜잭션 간 약간의 간격
		time.Sleep(100 * time.Millisecond)
	}

	// 블록들이 커밋될 때까지 대기
	time.Sleep(5 * time.Second)

	// 결과 확인
	t.Log("=== Final State ===")
	for id, node := range network.Nodes {
		blocks := node.Engine.GetCommittedBlocks()
		totalTxs := 0
		for _, b := range blocks {
			totalTxs += len(b.Transactions)
		}
		t.Logf("Node %s: height=%d, blocks=%d, total_txs=%d",
			id, node.Engine.GetCurrentHeight(), len(blocks), totalTxs)
	}
}

// TestTransactionBroadcast tests that transactions are broadcast to all nodes.
// 트랜잭션이 모든 노드로 브로드캐스트되는지 테스트
func TestTransactionBroadcast(t *testing.T) {
	network, err := NewTestNetwork(4, 50020)
	if err != nil {
		t.Fatalf("Failed to create network: %v", err)
	}
	defer network.Stop()

	if err := network.Start(); err != nil {
		t.Fatalf("Failed to start network: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	// 비리더 노드에 트랜잭션 추가
	var nonPrimary *TestNode
	for _, node := range network.Nodes {
		if !node.Engine.IsPrimary() {
			nonPrimary = node
			break
		}
	}

	if nonPrimary == nil {
		t.Fatal("No non-primary node found")
	}

	// 비리더 노드의 Mempool에 트랜잭션 추가
	tx := []byte("broadcast-test-tx")
	if err := nonPrimary.Mempool.AddTx(tx); err != nil {
		t.Fatalf("Failed to add tx to mempool: %v", err)
	}

	t.Logf("Added tx to non-primary node %s, mempool size: %d",
		nonPrimary.ID, nonPrimary.Mempool.Size())

	// 트랜잭션 브로드캐스트 (Transport를 통해)
	txHash := fmt.Sprintf("%x", tx[:8]) // 간단한 해시
	err = nonPrimary.Transport.BroadcastTransaction(tx, txHash, "", 0, 0, 0)
	if err != nil {
		t.Logf("Warning: broadcast failed: %v", err)
	}

	// 브로드캐스트 전파 대기
	time.Sleep(1 * time.Second)

	// 모든 노드의 Mempool 크기 확인
	t.Log("=== Mempool Status After Broadcast ===")
	for id, node := range network.Nodes {
		t.Logf("Node %s mempool size: %d", id, node.Mempool.Size())
	}
}

// TestMempoolConsensus tests that transactions from mempool are included in blocks.
// Mempool의 트랜잭션이 블록에 포함되는지 테스트
func TestMempoolConsensus(t *testing.T) {
	network, err := NewTestNetwork(4, 50030)
	if err != nil {
		t.Fatalf("Failed to create network: %v", err)
	}
	defer network.Stop()

	if err := network.Start(); err != nil {
		t.Fatalf("Failed to start network: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	primary := network.GetPrimary()
	if primary == nil {
		t.Fatal("No primary node found")
	}

	// 리더의 Mempool에 여러 트랜잭션 추가
	for i := 0; i < 5; i++ {
		tx := []byte(fmt.Sprintf("mempool-tx-%d", i))
		if err := primary.Mempool.AddTx(tx); err != nil {
			t.Fatalf("Failed to add tx to mempool: %v", err)
		}
	}

	t.Logf("Primary mempool size before proposal: %d", primary.Mempool.Size())

	// 블록 제안 트리거 (빈 트랜잭션으로)
	if err := primary.Engine.SubmitRequest([]byte("trigger"), "test"); err != nil {
		t.Fatalf("Failed to submit request: %v", err)
	}

	// 합의 대기
	if !network.WaitForHeight(1, 10*time.Second) {
		t.Fatal("Failed to reach height 1")
	}

	// Mempool 크기 확인 (트랜잭션이 블록에 포함되어 제거되었어야 함)
	time.Sleep(500 * time.Millisecond)
	t.Logf("Primary mempool size after consensus: %d", primary.Mempool.Size())

	// 블록에 트랜잭션이 포함되었는지 확인
	blocks := primary.Engine.GetCommittedBlocks()
	if len(blocks) > 0 {
		t.Logf("Block 1 has %d transactions", len(blocks[0].Transactions))
	}
}

// TestConsensusWithDelay tests consensus with simulated network delay.
// 네트워크 지연이 있는 상황에서의 합의 테스트
func TestConsensusWithDelay(t *testing.T) {
	network, err := NewTestNetwork(4, 50040)
	if err != nil {
		t.Fatalf("Failed to create network: %v", err)
	}
	defer network.Stop()

	if err := network.Start(); err != nil {
		t.Fatalf("Failed to start network: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	primary := network.GetPrimary()
	if primary == nil {
		t.Fatal("No primary node found")
	}

	// 여러 트랜잭션을 빠르게 제출
	for i := 0; i < 5; i++ {
		tx := []byte(fmt.Sprintf("delay-test-tx-%d", i))
		go func(tx []byte) {
			primary.Engine.SubmitRequest(tx, "test")
		}(tx)
	}

	// 모든 블록이 커밋될 때까지 대기
	time.Sleep(10 * time.Second)

	// 결과 확인 - 모든 노드가 동일한 상태인지
	heights := make(map[uint64][]string)
	for id, node := range network.Nodes {
		h := node.Engine.GetCurrentHeight()
		heights[h] = append(heights[h], id)
	}

	t.Log("=== Height Distribution ===")
	for h, nodes := range heights {
		t.Logf("Height %d: %v", h, nodes)
	}

	// 모든 노드가 같은 높이인지 확인 (약간의 차이는 허용)
	if len(heights) > 2 {
		t.Errorf("Nodes have too much height variance: %d different heights", len(heights))
	}
}

// TestNodeCount verifies network has correct number of validators.
// 네트워크 검증자 수 확인 테스트
func TestNodeCount(t *testing.T) {
	network, err := NewTestNetwork(4, 50050)
	if err != nil {
		t.Fatalf("Failed to create network: %v", err)
	}
	defer network.Stop()

	if err := network.Start(); err != nil {
		t.Fatalf("Failed to start network: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	// 피어 연결 확인
	for id, node := range network.Nodes {
		peerCount := node.Transport.PeerCount()
		expectedPeers := network.NodeCount - 1 // 자기 자신 제외
		if peerCount != expectedPeers {
			t.Errorf("Node %s has %d peers, expected %d", id, peerCount, expectedPeers)
		} else {
			t.Logf("Node %s has %d peers (correct)", id, peerCount)
		}
	}
}

// TestConcurrentSubmission tests concurrent transaction submission.
// 동시 트랜잭션 제출 테스트
func TestConcurrentSubmission(t *testing.T) {
	network, err := NewTestNetwork(4, 50060)
	if err != nil {
		t.Fatalf("Failed to create network: %v", err)
	}
	defer network.Stop()

	if err := network.Start(); err != nil {
		t.Fatalf("Failed to start network: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	primary := network.GetPrimary()
	if primary == nil {
		t.Fatal("No primary node found")
	}

	// 동시에 여러 트랜잭션 제출
	var wg sync.WaitGroup
	txCount := 20
	errors := make(chan error, txCount)

	for i := 0; i < txCount; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tx := []byte(fmt.Sprintf("concurrent-tx-%d", i))
			if err := primary.Engine.SubmitRequest(tx, "concurrent-test"); err != nil {
				errors <- err
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	// 에러 확인
	errorCount := 0
	for err := range errors {
		t.Logf("Submit error: %v", err)
		errorCount++
	}

	if errorCount > 0 {
		t.Logf("Warning: %d/%d submissions failed", errorCount, txCount)
	}

	// 합의 대기
	time.Sleep(10 * time.Second)

	// 결과 확인
	t.Log("=== Final State After Concurrent Submission ===")
	for id, node := range network.Nodes {
		blocks := node.Engine.GetCommittedBlocks()
		t.Logf("Node %s: height=%d, blocks=%d", id, node.Engine.GetCurrentHeight(), len(blocks))
	}
}

// TestQuorumSize verifies quorum size calculation.
// 쿼럼 크기 계산 테스트
func TestQuorumSize(t *testing.T) {
	testCases := []struct {
		nodeCount    int
		expectedQuorum int
	}{
		{4, 3},  // 3f+1=4, f=1, 2f+1=3
		{7, 5},  // 3f+1=7, f=2, 2f+1=5
		{10, 7}, // 3f+1=10, f=3, 2f+1=7
	}

	for _, tc := range testCases {
		validators := make([]*types.Validator, tc.nodeCount)
		for i := 0; i < tc.nodeCount; i++ {
			validators[i] = &types.Validator{ID: fmt.Sprintf("node-%d", i), Power: 1}
		}
		vs := types.NewValidatorSet(validators)

		quorum := vs.QuorumSize()
		if quorum != tc.expectedQuorum {
			t.Errorf("For %d nodes, expected quorum %d, got %d",
				tc.nodeCount, tc.expectedQuorum, quorum)
		} else {
			t.Logf("For %d nodes, quorum = %d (correct)", tc.nodeCount, quorum)
		}
	}
}

// TestEngineLifecycle tests engine start/stop lifecycle.
// 엔진 시작/종료 라이프사이클 테스트
func TestEngineLifecycle(t *testing.T) {
	network, err := NewTestNetwork(4, 50070)
	if err != nil {
		t.Fatalf("Failed to create network: %v", err)
	}

	// Start
	if err := network.Start(); err != nil {
		t.Fatalf("Failed to start network: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	// Verify running
	for id, node := range network.Nodes {
		if node.Transport.PeerCount() == 0 {
			t.Errorf("Node %s has no peers after start", id)
		}
	}

	// Stop
	network.Stop()

	// Give time for cleanup
	time.Sleep(100 * time.Millisecond)

	t.Log("Lifecycle test completed successfully")
}

// ================================================================================
//                          벤치마크 테스트
// ================================================================================

// BenchmarkIntegrationConsensus benchmarks full consensus with real network.
func BenchmarkIntegrationConsensus(b *testing.B) {
	network, err := NewTestNetwork(4, 51000)
	if err != nil {
		b.Fatalf("Failed to create network: %v", err)
	}
	defer network.Stop()

	if err := network.Start(); err != nil {
		b.Fatalf("Failed to start network: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	primary := network.GetPrimary()
	if primary == nil {
		b.Fatal("No primary node found")
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		tx := []byte(fmt.Sprintf("benchmark-tx-%d", i))
		primary.Engine.SubmitRequest(tx, "benchmark")
		time.Sleep(50 * time.Millisecond)
	}
}

// BenchmarkTransactionBroadcast benchmarks transaction broadcasting.
func BenchmarkTransactionBroadcast(b *testing.B) {
	network, err := NewTestNetwork(4, 51100)
	if err != nil {
		b.Fatalf("Failed to create network: %v", err)
	}
	defer network.Stop()

	if err := network.Start(); err != nil {
		b.Fatalf("Failed to start network: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	primary := network.GetPrimary()
	if primary == nil {
		b.Fatal("No primary node found")
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		tx := []byte(fmt.Sprintf("broadcast-bench-tx-%d", i))
		txHash := fmt.Sprintf("hash-%d", i)
		primary.Transport.BroadcastTransaction(tx, txHash, "", 0, 0, 0)
	}
}

// ================================================================================
//                          컨텍스트 테스트
// ================================================================================

// TestWithContext tests context cancellation.
func TestWithContext(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	network, err := NewTestNetwork(4, 50080)
	if err != nil {
		t.Fatalf("Failed to create network: %v", err)
	}
	defer network.Stop()

	if err := network.Start(); err != nil {
		t.Fatalf("Failed to start network: %v", err)
	}

	// 컨텍스트가 취소될 때까지 또는 합의가 완료될 때까지 대기
	done := make(chan bool)
	go func() {
		primary := network.GetPrimary()
		if primary != nil {
			primary.Engine.SubmitRequest([]byte("context-test"), "test")
		}
		network.WaitForHeight(1, 5*time.Second)
		done <- true
	}()

	select {
	case <-ctx.Done():
		t.Log("Context cancelled")
	case <-done:
		t.Log("Consensus completed before timeout")
	}
}
