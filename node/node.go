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
	"github.com/ahwlsqja/pbft-cosmos/metrics"
	"github.com/ahwlsqja/pbft-cosmos/transport"
)

// Node represents a PBFT consensus node.
type Node struct {
	mu sync.RWMutex

	config    *Config
	engine    *pbft.Engine
	transport *transport.GRPCTransport
	metrics   *metrics.Metrics

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
	validatorSet := pbft.NewValidatorSet(config.Validators)

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
		m = metrics.NewMetrics()
	}

	// Create PBFT engine
	engine, err := pbft.NewEngine(pbftConfig, validatorSet, trans, nil, m)
	if err != nil {
		return nil, fmt.Errorf("failed to create engine: %w", err)
	}

	return &Node{
		config:    config,
		engine:    engine,
		transport: trans,
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
	validatorSet := pbft.NewValidatorSet(config.Validators)

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
		m = metrics.NewMetrics()
	}

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
	engine, err := pbft.NewEngine(pbftConfig, validatorSet, trans, nil, m)
	if err != nil {
		return nil, fmt.Errorf("failed to create engine: %w", err)
	}
	_ = engineV2 // Use engineV2 when ABCI is connected

	return &Node{
		config:    config,
		engine:    engine,
		transport: trans,
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

	// Stop engine
	if err := n.engine.Stop(); err != nil {
		n.logger.Printf("[Node] Error stopping engine: %v", err)
	}

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
func (n *Node) SubmitTx(tx []byte, clientID string) error {
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
