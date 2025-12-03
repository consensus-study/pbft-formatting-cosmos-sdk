// Package node provides the PBFT node implementation.
package node

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/ahwlsqja/pbft-cosmos/consensus/pbft"
	"github.com/ahwlsqja/pbft-cosmos/mempool"
	"github.com/ahwlsqja/pbft-cosmos/metrics"
	"github.com/ahwlsqja/pbft-cosmos/transport"
	"github.com/ahwlsqja/pbft-cosmos/types"
)

// Node represents a PBFT consensus node.
type Node struct {
	mu sync.RWMutex

	config    *Config                   // 설정
	engine    *pbft.Engine              // PBFT 엔진
	transport *transport.GRPCTransport  // P2P 통신
	mempool   *mempool.Mempool          // 트랜잭션 풀
	reactor   *mempool.Reactor          // Mempool 네트워크 리액터
	metrics   *metrics.Metrics          // 매트릭

	// State
	running bool
	done    chan struct{}

	// Logger
	logger *log.Logger

	// Metrics HTTP server
	metricsServer *http.Server
}

// NewNode creates a new PBFT node.
func NewNode(config *Config) (*Node, error) {
	// Validate config
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	// Create gRPC transport
	trans, err := transport.NewGRPCTransport(config.NodeID, config.ListenAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to create transport: %w", err)
	}

	// Create validator set
	validatorSet := types.NewValidatorSet(config.Validators)

	// Create PBFT configuration
	pbftConfig := &pbft.Config{
		NodeID:             config.NodeID,
		RequestTimeout:     config.RequestTimeout,
		ViewChangeTimeout:  config.ViewChangeTimeout,
		CheckpointInterval: config.CheckpointInterval,
		WindowSize:         config.WindowSize,
	}

	// Create metrics
	var m *metrics.Metrics
	if config.MetricsEnabled {
		m = metrics.NewMetrics("pbft")
	}

	// Create Mempool
	mempoolConfig := mempool.DefaultConfig()
	mp := mempool.NewMempool(mempoolConfig)

	// Create Mempool Reactor
	reactorConfig := mempool.DefaultReactorConfig()
	reactor := mempool.NewReactor(mp, reactorConfig)

	// Create PBFT engine
	engine := pbft.NewEngine(pbftConfig, validatorSet, trans, nil, m)

	// Connect Mempool to Engine
	engine.SetMempool(mp)

	return &Node{
		config:    config,
		engine:    engine,
		transport: trans,
		mempool:   mp,
		reactor:   reactor,
		metrics:   m,
		done:      make(chan struct{}),
		logger:    log.Default(),
	}, nil
}

// NewNodeWithABCI creates a new PBFT node with ABCI adapter.
func NewNodeWithABCI(config *Config) (*Node, error) {
	// Validate config
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	// Create gRPC transport
	trans, err := transport.NewGRPCTransport(config.NodeID, config.ListenAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to create transport: %w", err)
	}

	// Create validator set
	validatorSet := types.NewValidatorSet(config.Validators)

	// Create PBFT configuration
	pbftConfig := &pbft.Config{
		NodeID:             config.NodeID,
		RequestTimeout:     config.RequestTimeout,
		ViewChangeTimeout:  config.ViewChangeTimeout,
		CheckpointInterval: config.CheckpointInterval,
		WindowSize:         config.WindowSize,
	}

	// Create metrics
	var m *metrics.Metrics
	if config.MetricsEnabled {
		m = metrics.NewMetrics("pbft")
	}

	// Create Mempool
	mempoolConfig := mempool.DefaultConfig()
	mp := mempool.NewMempool(mempoolConfig)

	// Create Mempool Reactor
	reactorConfig := mempool.DefaultReactorConfig()
	reactor := mempool.NewReactor(mp, reactorConfig)

	// Create ABCI adapter
	abciAdapter, err := pbft.NewABCIAdapter(config.ABCIAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to create ABCI adapter: %w", err)
	}

	// Create PBFT engine V2 with ABCI support
	engineV2, err := pbft.NewEngineV2(pbftConfig, validatorSet, trans, abciAdapter, m)
	if err != nil {
		abciAdapter.Close()
		return nil, fmt.Errorf("failed to create engine v2: %w", err)
	}

	// We need to wrap engineV2 for compatibility
	// For now, use the regular engine for basic functionality
	engine := pbft.NewEngine(pbftConfig, validatorSet, trans, nil, m)
	_ = engineV2 // Use engineV2 when ABCI is connected

	// Connect Mempool to Engine
	engine.SetMempool(mp)

	return &Node{
		config:    config,
		engine:    engine,
		transport: trans,
		mempool:   mp,
		reactor:   reactor,
		metrics:   m,
		done:      make(chan struct{}),
		logger:    log.Default(),
	}, nil
}

// Start starts the PBFT node.
func (n *Node) Start(ctx context.Context) error {
	n.mu.Lock()
	if n.running {
		n.mu.Unlock()
		return fmt.Errorf("node already running")
	}
	n.running = true
	n.mu.Unlock()

	n.logger.Printf("[Node] Starting PBFT node %s", n.config.NodeID)

	// Start transport
	if err := n.transport.Start(); err != nil {
		return fmt.Errorf("failed to start transport: %w", err)
	}
	n.logger.Printf("[Node] Transport started on %s", n.config.ListenAddr)

	// Connect to peers
	for _, peerStr := range n.config.Peers {
		parts := strings.SplitN(peerStr, "@", 2)
		if len(parts) != 2 {
			n.logger.Printf("[Node] Invalid peer format: %s (expected nodeID@address)", peerStr)
			continue
		}
		peerID, peerAddr := parts[0], parts[1]

		// Skip self
		if peerID == n.config.NodeID {
			continue
		}

		if err := n.transport.AddPeer(peerID, peerAddr); err != nil {
			n.logger.Printf("[Node] Failed to connect to peer %s: %v", peerID, err)
		} else {
			n.logger.Printf("[Node] Connected to peer %s at %s", peerID, peerAddr)
		}
	}

	// Start Mempool
	if n.mempool != nil {
		if err := n.mempool.Start(); err != nil {
			return fmt.Errorf("failed to start mempool: %w", err)
		}
		n.logger.Printf("[Node] Mempool started")
	}

	// Start Reactor (트랜잭션 브로드캐스트용)
	if n.reactor != nil {
		// Reactor에 브로드캐스터 설정 (Transport를 래핑)
		n.reactor.SetBroadcaster(&transportBroadcaster{transport: n.transport})
		if err := n.reactor.Start(); err != nil {
			return fmt.Errorf("failed to start reactor: %w", err)
		}
		n.logger.Printf("[Node] Mempool reactor started")
	}

	// Start metrics server if enabled
	if n.config.MetricsEnabled && n.metrics != nil {
		go n.startMetricsServer()
	}

	// Start PBFT engine
	if err := n.engine.Start(); err != nil {
		return fmt.Errorf("failed to start engine: %w", err)
	}

	n.logger.Printf("[Node] PBFT node %s started successfully", n.config.NodeID)
	n.logger.Printf("[Node]   Chain ID: %s", n.config.ChainID)
	n.logger.Printf("[Node]   P2P address: %s", n.config.ListenAddr)
	n.logger.Printf("[Node]   ABCI address: %s", n.config.ABCIAddr)
	n.logger.Printf("[Node]   Validators: %d", len(n.config.Validators))
	n.logger.Printf("[Node]   Mempool: enabled")
	n.logger.Printf("[Node]   Metrics: %s", n.config.MetricsAddr)

	return nil
}

// Stop stops the PBFT node.
func (n *Node) Stop() error {
	n.mu.Lock()
	if !n.running {
		n.mu.Unlock()
		return nil
	}
	n.running = false
	n.mu.Unlock()

	n.logger.Printf("[Node] Stopping PBFT node %s", n.config.NodeID)

	close(n.done)

	// Stop metrics server
	if n.metricsServer != nil {
		n.metricsServer.Close()
	}

	// Stop reactor
	if n.reactor != nil {
		n.reactor.Stop()
	}

	// Stop mempool
	if n.mempool != nil {
		n.mempool.Stop()
	}

	// Stop engine
	n.engine.Stop()

	// Stop transport
	n.transport.Stop()

	n.logger.Printf("[Node] PBFT node %s stopped", n.config.NodeID)
	return nil
}

// startMetricsServer starts the Prometheus metrics server.
func (n *Node) startMetricsServer() {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())

	// Add health check endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	// Add status endpoint
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		n.mu.RLock()
		running := n.running
		n.mu.RUnlock()

		status := fmt.Sprintf(`{
			"node_id": "%s",
			"chain_id": "%s",
			"running": %t,
			"view": %d,
			"height": %d,
			"peers": %d
		}`,
			n.config.NodeID,
			n.config.ChainID,
			running,
			n.engine.GetCurrentView(),
			n.engine.GetCurrentHeight(),
			n.transport.PeerCount(),
		)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(status))
	})

	n.metricsServer = &http.Server{
		Addr:    n.config.MetricsAddr,
		Handler: mux,
	}

	n.logger.Printf("[Node] Metrics server started on %s", n.config.MetricsAddr)

	if err := n.metricsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		n.logger.Printf("[Node] Metrics server error: %v", err)
	}
}

// SubmitTx submits a transaction to the node.
// 트랜잭션을 Mempool에 추가하고, 리더인 경우 블록 제안을 트리거합니다.
func (n *Node) SubmitTx(tx []byte, clientID string) error {
	// Mempool이 있으면 먼저 추가
	if n.mempool != nil {
		if err := n.mempool.AddTxWithMeta(tx, clientID, 0, 0, 0); err != nil {
			return fmt.Errorf("failed to add tx to mempool: %w", err)
		}
		n.logger.Printf("[Node] Transaction added to mempool (size: %d)", n.mempool.Size())

		// 리더인 경우 블록 제안 트리거
		if n.engine.IsPrimary() {
			return n.engine.SubmitRequest(tx, clientID)
		}
		return nil
	}

	// Mempool 없으면 직접 엔진에 전달
	return n.engine.SubmitRequest(tx, clientID)
}

// GetHeight returns the current block height.
func (n *Node) GetHeight() uint64 {
	return n.engine.GetCurrentHeight()
}

// GetView returns the current view number.
func (n *Node) GetView() uint64 {
	return n.engine.GetCurrentView()
}

// GetPeerCount returns the number of connected peers.
func (n *Node) GetPeerCount() int {
	return n.transport.PeerCount()
}

// IsPrimary returns true if this node is the current primary.
func (n *Node) IsPrimary() bool {
	return n.engine.IsPrimary()
}

// GetNodeID returns the node ID.
func (n *Node) GetNodeID() string {
	return n.config.NodeID
}

// GetChainID returns the chain ID.
func (n *Node) GetChainID() string {
	return n.config.ChainID
}

// IsRunning returns true if the node is running.
func (n *Node) IsRunning() bool {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.running
}

// GetMempool returns the mempool.
func (n *Node) GetMempool() *mempool.Mempool {
	return n.mempool
}

// GetMempoolSize returns the current mempool size.
func (n *Node) GetMempoolSize() int {
	if n.mempool != nil {
		return n.mempool.Size()
	}
	return 0
}

// transportBroadcaster adapts GRPCTransport to mempool.Broadcaster interface.
// Transport를 Mempool Broadcaster 인터페이스에 맞게 래핑합니다.
type transportBroadcaster struct {
	transport *transport.GRPCTransport
}

// BroadcastTx broadcasts a transaction to all peers.
func (b *transportBroadcaster) BroadcastTx(tx []byte) error {
	// TODO: 트랜잭션 전용 메시지 타입 추가 필요
	// 현재는 간단히 로그만 출력
	// 실제 구현에서는 트랜잭션 메시지를 생성하여 브로드캐스트해야 함
	return nil
}

// SendTx sends a transaction to a specific peer.
func (b *transportBroadcaster) SendTx(peerID string, tx []byte) error {
	// TODO: 특정 피어에게 트랜잭션 전송
	return nil
}
