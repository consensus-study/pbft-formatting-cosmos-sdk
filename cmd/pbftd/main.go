// Package main provides the entry point for the PBFT consensus daemon.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/ahwlsqja/pbft-cosmos/node"
	"github.com/ahwlsqja/pbft-cosmos/types"
)

var rootCmd = &cobra.Command{
	Use:   "pbftd",
	Short: "PBFT consensus daemon for Cosmos SDK v0.53.0",
	Long: `PBFT consensus daemon implementing Practical Byzantine Fault Tolerance
for integration with Cosmos SDK v0.53.0 and CometBFT v0.38.x ABCI 2.0.

This daemon can be used to run a PBFT consensus network either standalone
or integrated with a Cosmos SDK application.`,
}

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the PBFT node",
	Long:  `Start the PBFT consensus node and connect to the network.`,
	RunE:  runStart,
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Get node status",
	RunE:  runStatus,
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("pbftd v0.1.0")
		fmt.Println("PBFT Consensus for Cosmos SDK v0.53.0")
		fmt.Println("Built with Go 1.22+")
	},
}

func init() {
	// Add commands
	rootCmd.AddCommand(startCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(versionCmd)

	// Start command flags
	startCmd.Flags().String("node-id", "", "Unique node identifier (required)")
	startCmd.Flags().String("chain-id", "pbft-chain", "Chain ID")
	startCmd.Flags().String("listen", "0.0.0.0:26656", "P2P listen address")
	startCmd.Flags().String("abci", "localhost:26658", "ABCI application address")
	startCmd.Flags().StringSlice("peers", []string{}, "Peer addresses (format: nodeID@address)")
	startCmd.Flags().String("metrics", "0.0.0.0:26660", "Prometheus metrics address")
	startCmd.Flags().Bool("metrics-enabled", true, "Enable Prometheus metrics")
	startCmd.Flags().Duration("request-timeout", 5*time.Second, "Request timeout")
	startCmd.Flags().Duration("view-change-timeout", 10*time.Second, "View change timeout")
	startCmd.Flags().Uint64("checkpoint-interval", 100, "Checkpoint interval")
	startCmd.Flags().Uint64("window-size", 200, "PBFT window size")
	startCmd.Flags().String("validators", "", "Validator configuration JSON file")
	startCmd.Flags().String("config", "", "Configuration file path")
	startCmd.Flags().Bool("benchmark", false, "Run in benchmark mode")
	startCmd.Flags().Int("benchmark-tx-count", 1000, "Number of transactions for benchmark")

	// Bind flags to viper
	viper.BindPFlags(startCmd.Flags())
}

func runStart(cmd *cobra.Command, args []string) error {
	// Load config file if specified
	configFile := viper.GetString("config")
	if configFile != "" {
		viper.SetConfigFile(configFile)
		if err := viper.ReadInConfig(); err != nil {
			return fmt.Errorf("failed to read config file: %w", err)
		}
	}

	// Build configuration
	config := &node.Config{
		NodeID:             viper.GetString("node-id"),
		ChainID:            viper.GetString("chain-id"),
		ListenAddr:         viper.GetString("listen"),
		ABCIAddr:           viper.GetString("abci"),
		Peers:              viper.GetStringSlice("peers"),
		RequestTimeout:     viper.GetDuration("request-timeout"),
		ViewChangeTimeout:  viper.GetDuration("view-change-timeout"),
		CheckpointInterval: viper.GetUint64("checkpoint-interval"),
		WindowSize:         viper.GetUint64("window-size"),
		MetricsEnabled:     viper.GetBool("metrics-enabled"),
		MetricsAddr:        viper.GetString("metrics"),
	}

	// Validate node ID
	if config.NodeID == "" {
		return fmt.Errorf("--node-id is required")
	}

	// Load validators from file or use defaults
	validatorsFile := viper.GetString("validators")
	if validatorsFile != "" {
		validators, err := loadValidatorsFromFile(validatorsFile)
		if err != nil {
			return fmt.Errorf("failed to load validators: %w", err)
		}
		config.Validators = validators
	} else {
		// Default 4-node validator set for testing
		config.Validators = createDefaultValidators()
	}

	// Create node
	n, err := node.NewNode(config)
	if err != nil {
		return fmt.Errorf("failed to create node: %w", err)
	}

	// Create context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start node
	if err := n.Start(ctx); err != nil {
		return fmt.Errorf("failed to start node: %w", err)
	}

	// Print startup info
	fmt.Println("========================================")
	fmt.Printf("  PBFT Node Started\n")
	fmt.Printf("  Node ID:    %s\n", config.NodeID)
	fmt.Printf("  Chain ID:   %s\n", config.ChainID)
	fmt.Printf("  P2P Addr:   %s\n", config.ListenAddr)
	fmt.Printf("  ABCI Addr:  %s\n", config.ABCIAddr)
	fmt.Printf("  Metrics:    %s\n", config.MetricsAddr)
	fmt.Printf("  Validators: %d\n", len(config.Validators))
	fmt.Println("========================================")

	// Run benchmark if requested
	if viper.GetBool("benchmark") {
		go runBenchmark(n, viper.GetInt("benchmark-tx-count"))
	}

	// Wait for interrupt signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	fmt.Println("\nShutting down...")
	return n.Stop()
}

func runStatus(cmd *cobra.Command, args []string) error {
	// This would typically connect to a running node
	// For now, just print a placeholder
	fmt.Println("Status: Not connected to node")
	fmt.Println("Use 'pbftd start' to start a node")
	return nil
}

func loadValidatorsFromFile(path string) ([]*types.Validator, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var validators []*types.Validator
	if err := json.Unmarshal(data, &validators); err != nil {
		return nil, err
	}

	return validators, nil
}

func createDefaultValidators() []*types.Validator {
	// Default 4-node validator set for testing
	return []*types.Validator{
		{ID: "node0", PublicKey: []byte("pubkey0"), Power: 10},
		{ID: "node1", PublicKey: []byte("pubkey1"), Power: 10},
		{ID: "node2", PublicKey: []byte("pubkey2"), Power: 10},
		{ID: "node3", PublicKey: []byte("pubkey3"), Power: 10},
	}
}

func runBenchmark(n *node.Node, txCount int) {
	log.Printf("[Benchmark] Starting benchmark with %d transactions...", txCount)

	// Wait for network to stabilize
	time.Sleep(3 * time.Second)

	startTime := time.Now()

	// Submit transactions
	for i := 0; i < txCount; i++ {
		tx := fmt.Sprintf(`{"type":"set","key":"key-%d","value":"value-%d"}`, i, i)

		if err := n.SubmitTx([]byte(tx), "benchmark-client"); err != nil {
			log.Printf("[Benchmark] Failed to submit tx %d: %v", i, err)
		}

		// Small delay to avoid overwhelming
		if i > 0 && i%100 == 0 {
			time.Sleep(10 * time.Millisecond)
			log.Printf("[Benchmark] Submitted %d transactions", i)
		}
	}

	// Wait for transactions to be processed
	time.Sleep(5 * time.Second)

	elapsed := time.Since(startTime)
	tps := float64(txCount) / elapsed.Seconds()

	log.Println("[Benchmark] ========================================")
	log.Printf("[Benchmark] Total transactions: %d", txCount)
	log.Printf("[Benchmark] Total time: %v", elapsed)
	log.Printf("[Benchmark] TPS: %.2f", tps)
	log.Printf("[Benchmark] Final height: %d", n.GetHeight())
	log.Println("[Benchmark] ========================================")
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// Helper function for parsing peers (for backwards compatibility)
func parsePeers(peersStr string) []string {
	if peersStr == "" {
		return nil
	}

	var peers []string
	for _, peer := range strings.Split(peersStr, ",") {
		peer = strings.TrimSpace(peer)
		if peer != "" {
			peers = append(peers, peer)
		}
	}
	return peers
}
