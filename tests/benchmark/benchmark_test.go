// Package benchmark provides performance benchmarks for the PBFT consensus engine.
package benchmark

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/ahwlsqja/pbft-cosmos/abci"
	"github.com/ahwlsqja/pbft-cosmos/consensus/pbft"
	"github.com/ahwlsqja/pbft-cosmos/crypto"
	_ "github.com/ahwlsqja/pbft-cosmos/network" // 아직 다른 테스트에서 사용될 수 있음
	"github.com/ahwlsqja/pbft-cosmos/types"
)

// BenchmarkBlockCreation benchmarks block creation.
func BenchmarkBlockCreation(b *testing.B) {
	txs := make([]types.Transaction, 100)
	for i := range txs {
		txs[i] = types.Transaction{
			ID:   fmt.Sprintf("tx-%d", i),
			Data: []byte(fmt.Sprintf("data-%d", i)),
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = types.NewBlock(uint64(i), nil, "proposer", 0, txs)
	}
}

// BenchmarkBlockHash benchmarks block hash computation.
func BenchmarkBlockHash(b *testing.B) {
	txs := make([]types.Transaction, 100)
	for i := range txs {
		txs[i] = types.Transaction{
			ID:   fmt.Sprintf("tx-%d", i),
			Data: []byte(fmt.Sprintf("data-%d", i)),
		}
	}
	block := types.NewBlock(1, nil, "proposer", 0, txs)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = block.ComputeHash()
	}
}

// BenchmarkMessageEncoding benchmarks message encoding.
func BenchmarkMessageEncoding(b *testing.B) {
	msg := pbft.NewMessage(pbft.PrePrepare, 0, 1, []byte("digest"), "node1")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = msg.Encode()
	}
}

// BenchmarkMessageDecoding benchmarks message decoding.
func BenchmarkMessageDecoding(b *testing.B) {
	msg := pbft.NewMessage(pbft.PrePrepare, 0, 1, []byte("digest"), "node1")
	data, _ := msg.Encode()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = pbft.DecodeMessage(data)
	}
}

// BenchmarkStateAddPrepare benchmarks adding prepare messages.
func BenchmarkStateAddPrepare(b *testing.B) {
	state := pbft.NewState(0, 1)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		prepare := pbft.NewPrepareMsg(0, 1, []byte("digest"), fmt.Sprintf("node-%d", i))
		state.AddPrepare(prepare)
	}
}

// BenchmarkKeyGeneration benchmarks key pair generation.
func BenchmarkKeyGeneration(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_, _ = crypto.GenerateKeyPair()
	}
}

// BenchmarkSigning benchmarks message signing.
func BenchmarkSigning(b *testing.B) {
	kp, _ := crypto.GenerateKeyPair()
	message := []byte("test message to sign")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = kp.Sign(message)
	}
}

// BenchmarkVerification benchmarks signature verification.
func BenchmarkVerification(b *testing.B) {
	kp, _ := crypto.GenerateKeyPair()
	message := []byte("test message to sign")
	sig, _ := kp.Sign(message)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = crypto.Verify(kp.PublicKey, message, sig)
	}
}

// BenchmarkHash benchmarks SHA256 hashing.
func BenchmarkHash(b *testing.B) {
	data := make([]byte, 1024)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = crypto.Hash(data)
	}
}

// BenchmarkMerkleRoot benchmarks Merkle root computation.
func BenchmarkMerkleRoot(b *testing.B) {
	hashes := make([][]byte, 100)
	for i := range hashes {
		hashes[i] = crypto.Hash([]byte(fmt.Sprintf("data-%d", i)))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = crypto.MerkleRoot(hashes)
	}
}

// BenchmarkAppExecuteBlock benchmarks block execution.
func BenchmarkAppExecuteBlock(b *testing.B) {
	app := abci.NewApplication()

	txs := make([]types.Transaction, 100)
	for i := range txs {
		op := abci.Operation{
			Type:  "set",
			Key:   fmt.Sprintf("key-%d", i),
			Value: fmt.Sprintf("value-%d", i),
		}
		data, _ := json.Marshal(op)
		txs[i] = types.Transaction{
			ID:   fmt.Sprintf("tx-%d", i),
			Data: data,
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		block := types.NewBlock(uint64(i), nil, "proposer", 0, txs)
		_, _ = app.ExecuteBlock(block)
		_ = app.Commit(block)
	}
}

// SimulatedNetwork provides a simulated network for testing.
type SimulatedNetwork struct {
	nodes     map[string]*pbft.EngineV2
	transport map[string]*SimulatedTransport
	mu        sync.RWMutex
}

// SimulatedTransport simulates network transport.
type SimulatedTransport struct {
	nodeID  string
	network *SimulatedNetwork
	handler func(*pbft.Message)
}

func (st *SimulatedTransport) Broadcast(msg *pbft.Message) error {
	st.network.mu.RLock()
	defer st.network.mu.RUnlock()

	for id, transport := range st.network.transport {
		if id != st.nodeID && transport.handler != nil {
			// Simulate async delivery
			go func(t *SimulatedTransport, m *pbft.Message) {
				t.handler(m)
			}(transport, msg)
		}
	}
	return nil
}

func (st *SimulatedTransport) Send(nodeID string, msg *pbft.Message) error {
	st.network.mu.RLock()
	transport, exists := st.network.transport[nodeID]
	st.network.mu.RUnlock()

	if exists && transport.handler != nil {
		go transport.handler(msg)
	}
	return nil
}

func (st *SimulatedTransport) SetMessageHandler(handler func(*pbft.Message)) {
	st.handler = handler
}

// NewSimulatedNetwork creates a new simulated network.
func NewSimulatedNetwork(nodeCount int) *SimulatedNetwork {
	sn := &SimulatedNetwork{
		nodes:     make(map[string]*pbft.EngineV2),
		transport: make(map[string]*SimulatedTransport),
	}

	// Create validators
	validators := make([]*types.Validator, nodeCount)
	for i := 0; i < nodeCount; i++ {
		validators[i] = &types.Validator{
			ID:    fmt.Sprintf("node-%d", i),
			Power: 1,
		}
	}
	vs := types.NewValidatorSet(validators)

	// Create nodes
	for i := 0; i < nodeCount; i++ {
		nodeID := fmt.Sprintf("node-%d", i)

		st := &SimulatedTransport{
			nodeID:  nodeID,
			network: sn,
		}
		sn.transport[nodeID] = st

		config := pbft.DefaultConfig(nodeID)
		config.ViewChangeTimeout = 30 * time.Second // Longer timeout for tests

		// Use NoopABCIAdapter instead of Application (EngineV2 requires ABCI adapter)
		noopAdapter := pbft.NewNoopABCIAdapter()

		engine, _ := pbft.NewEngineV2(config, vs, st, noopAdapter, nil)
		sn.nodes[nodeID] = engine
	}

	return sn
}

// Start starts all nodes.
func (sn *SimulatedNetwork) Start() {
	for _, engine := range sn.nodes {
		engine.Start()
	}
}

// Stop stops all nodes.
func (sn *SimulatedNetwork) Stop() {
	for _, engine := range sn.nodes {
		engine.Stop()
	}
}

// BenchmarkConsensusRound benchmarks a full consensus round.
func BenchmarkConsensusRound(b *testing.B) {
	// Create a 4-node network
	sn := NewSimulatedNetwork(4)
	sn.Start()
	defer sn.Stop()

	// Wait for network to stabilize
	time.Sleep(100 * time.Millisecond)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Submit transaction to primary
		op := abci.Operation{
			Type:  "set",
			Key:   fmt.Sprintf("key-%d", i),
			Value: fmt.Sprintf("value-%d", i),
		}
		data, _ := json.Marshal(op)

		sn.nodes["node-0"].SubmitRequest(data, "benchmark")

		// Wait for consensus
		time.Sleep(50 * time.Millisecond)
	}
}

// TestThroughput measures transaction throughput.
func TestThroughput(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping throughput test in short mode")
	}

	// Create a 4-node network
	sn := NewSimulatedNetwork(4)
	sn.Start()
	defer sn.Stop()

	// Wait for network to stabilize
	time.Sleep(500 * time.Millisecond)

	txCount := 1000
	startTime := time.Now()

	// Submit transactions
	for i := 0; i < txCount; i++ {
		op := abci.Operation{
			Type:  "set",
			Key:   fmt.Sprintf("key-%d", i),
			Value: fmt.Sprintf("value-%d", i),
		}
		data, _ := json.Marshal(op)

		sn.nodes["node-0"].SubmitRequest(data, "throughput-test")

		// Small delay to avoid overwhelming
		if i%100 == 0 {
			time.Sleep(10 * time.Millisecond)
		}
	}

	// Wait for all transactions
	time.Sleep(5 * time.Second)

	elapsed := time.Since(startTime)
	tps := float64(txCount) / elapsed.Seconds()

	t.Logf("Throughput Test Results:")
	t.Logf("  Transactions: %d", txCount)
	t.Logf("  Time: %v", elapsed)
	t.Logf("  TPS: %.2f", tps)
}

// TestLatency measures consensus latency.
func TestLatency(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping latency test in short mode")
	}

	// Create a 4-node network
	sn := NewSimulatedNetwork(4)
	sn.Start()
	defer sn.Stop()

	// Wait for network to stabilize
	time.Sleep(500 * time.Millisecond)

	iterations := 100
	var totalLatency time.Duration

	for i := 0; i < iterations; i++ {
		op := abci.Operation{
			Type:  "set",
			Key:   fmt.Sprintf("latency-key-%d", i),
			Value: fmt.Sprintf("latency-value-%d", i),
		}
		data, _ := json.Marshal(op)

		startHeight := sn.nodes["node-0"].GetCurrentHeight()
		startTime := time.Now()

		sn.nodes["node-0"].SubmitRequest(data, "latency-test")

		// Wait for block to be committed
		for j := 0; j < 100; j++ {
			if sn.nodes["node-0"].GetCurrentHeight() > startHeight {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}

		latency := time.Since(startTime)
		totalLatency += latency
	}

	avgLatency := totalLatency / time.Duration(iterations)
	t.Logf("Latency Test Results:")
	t.Logf("  Iterations: %d", iterations)
	t.Logf("  Average Latency: %v", avgLatency)
}
