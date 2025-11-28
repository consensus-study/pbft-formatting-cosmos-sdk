// Package main provides the entry point for the PBFT consensus daemon.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/ahwlsqja/pbft-cosmos/abci"
	"github.com/ahwlsqja/pbft-cosmos/consensus/pbft"
	"github.com/ahwlsqja/pbft-cosmos/crypto"
	"github.com/ahwlsqja/pbft-cosmos/metrics"
	"github.com/ahwlsqja/pbft-cosmos/network"
	"github.com/ahwlsqja/pbft-cosmos/types"
)

var (
	nodeID      = flag.String("node-id", "", "Unique node identifier")
	listenAddr  = flag.String("listen", ":26656", "P2P listen address")
	metricsAddr = flag.String("metrics", ":26660", "Prometheus metrics address")
	peers       = flag.String("peers", "", "Comma-separated list of peer addresses (id@host:port)")
	benchmark   = flag.Bool("benchmark", false, "Run benchmark mode")
	txCount     = flag.Int("tx-count", 1000, "Number of transactions for benchmark")
)

func main() {
	flag.Parse()

	// Validate node ID
	if *nodeID == "" {
		// Generate a random node ID
		signer, err := crypto.NewDefaultSigner()
		if err != nil {
			log.Fatalf("Failed to generate signer: %v", err)
		}
		*nodeID = signer.Address()[:8]
	}

	log.Printf("Starting PBFT node: %s", *nodeID)

	// Initialize metrics
	m := metrics.NewMetrics("pbft")
	metricsServer := metrics.NewServer(*metricsAddr)
	if err := metricsServer.Start(); err != nil {
		log.Fatalf("Failed to start metrics server: %v", err)
	}
	log.Printf("Metrics server listening on %s", *metricsAddr)

	// Parse peers
	peerConfigs := parsePeers(*peers)

	// Create validator set
	validatorSet := createValidatorSet(*nodeID, peerConfigs)

	// Initialize transport
	transportConfig := &network.TransportConfig{
		NodeID:  *nodeID,
		Address: *listenAddr,
		Peers:   peerConfigs,
	}
	transport := network.NewTransport(transportConfig)

	// Initialize application
	app := abci.NewApplication()

	// Initialize PBFT engine
	engineConfig := pbft.DefaultConfig(*nodeID)
	engine := pbft.NewEngine(engineConfig, validatorSet, transport, app, m)

	// Start transport
	if err := transport.Start(); err != nil {
		log.Fatalf("Failed to start transport: %v", err)
	}

	// Connect to peers
	for _, peer := range peerConfigs {
		if err := transport.Connect(peer.ID, peer.Address); err != nil {
			log.Printf("Failed to connect to peer %s: %v", peer.ID, err)
		}
	}

	// Start PBFT engine
	if err := engine.Start(); err != nil {
		log.Fatalf("Failed to start PBFT engine: %v", err)
	}

	// Run benchmark if requested
	if *benchmark {
		go runBenchmark(engine, app, *txCount)
	}

	// Wait for interrupt signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("Shutting down...")

	// Stop components
	engine.Stop()
	transport.Stop()
	metricsServer.Stop()

	log.Println("Shutdown complete")
}

func parsePeers(peersStr string) []network.PeerConfig {
	if peersStr == "" {
		return nil
	}

	var configs []network.PeerConfig
	for _, peer := range strings.Split(peersStr, ",") {
		peer = strings.TrimSpace(peer)
		if peer == "" {
			continue
		}

		parts := strings.Split(peer, "@")
		if len(parts) != 2 {
			log.Printf("Invalid peer format: %s (expected id@host:port)", peer)
			continue
		}

		configs = append(configs, network.PeerConfig{
			ID:      parts[0],
			Address: parts[1],
		})
	}

	return configs
}

func createValidatorSet(selfID string, peers []network.PeerConfig) *types.ValidatorSet {
	validators := make([]*types.Validator, 0)

	// Add self
	validators = append(validators, &types.Validator{
		ID:      selfID,
		Address: *listenAddr,
		Power:   1,
	})

	// Add peers
	for _, peer := range peers {
		validators = append(validators, &types.Validator{
			ID:      peer.ID,
			Address: peer.Address,
			Power:   1,
		})
	}

	return types.NewValidatorSet(validators)
}

func runBenchmark(engine *pbft.Engine, app *abci.Application, txCount int) {
	log.Printf("Starting benchmark with %d transactions...", txCount)

	// Wait for network to stabilize
	time.Sleep(2 * time.Second)

	startTime := time.Now()

	// Submit transactions
	for i := 0; i < txCount; i++ {
		op := abci.Operation{
			Type:  "set",
			Key:   fmt.Sprintf("key-%d", i),
			Value: fmt.Sprintf("value-%d", i),
		}
		data, _ := json.Marshal(op)

		if err := engine.SubmitRequest(data, "benchmark-client"); err != nil {
			log.Printf("Failed to submit tx %d: %v", i, err)
		}

		// Small delay to avoid overwhelming
		if i%100 == 0 {
			time.Sleep(10 * time.Millisecond)
		}
	}

	// Wait for all transactions to be processed
	time.Sleep(5 * time.Second)

	elapsed := time.Since(startTime)
	tps := float64(txCount) / elapsed.Seconds()

	log.Printf("Benchmark complete!")
	log.Printf("  Total transactions: %d", txCount)
	log.Printf("  Total time: %v", elapsed)
	log.Printf("  TPS: %.2f", tps)
	log.Printf("  Final height: %d", engine.GetCurrentHeight())
}
