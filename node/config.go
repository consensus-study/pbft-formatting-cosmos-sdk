// Package node provides the PBFT node implementation.
package node

import (
	"time"

	"github.com/ahwlsqja/pbft-cosmos/consensus/pbft"
)

// Config holds configuration for a PBFT node.
type Config struct {
	// 노드 식별
	NodeID  string // "node0", "node1"
	ChainID string // "pbft-chain"

	// 네트워크 주소
	ListenAddr string // PBFT P2P listen address
	ABCIAddr   string // ABCI app address

	// 피어 목록
	Peers []string

	// 검증자 목록
	Validators []*pbft.ValidatorInfo

	// 타이밍
	RequestTimeout     time.Duration
	ViewChangeTimeout  time.Duration
	CheckpointInterval uint64
	WindowSize         uint64

	// Prometheus metrics
	MetricsEnabled bool
	MetricsAddr    string

	// Logging
	LogLevel string

	// Data directory
	DataDir string
}

// DefaultConfig returns a default configuration.
func DefaultConfig() *Config {
	return &Config{
		NodeID:             "",
		ChainID:            "pbft-chain",
		ListenAddr:         "0.0.0.0:26656",
		ABCIAddr:           "localhost:26658",
		Peers:              []string{},
		Validators:         []*pbft.ValidatorInfo{},
		RequestTimeout:     5 * time.Second,
		ViewChangeTimeout:  10 * time.Second,
		CheckpointInterval: 100,
		WindowSize:         200,
		MetricsEnabled:     true,
		MetricsAddr:        "0.0.0.0:26660",
		LogLevel:           "info",
		DataDir:            "./data",
	}
}

// Validate validates the configuration.
func (c *Config) Validate() error {
	if c.NodeID == "" {
		return ErrEmptyNodeID
	}
	if c.ChainID == "" {
		return ErrEmptyChainID
	}
	if c.ListenAddr == "" {
		return ErrEmptyListenAddr
	}
	if len(c.Validators) < 4 {
		return ErrInsufficientValidators
	}
	return nil
}

// Custom errors
type configError string

func (e configError) Error() string {
	return string(e)
}

const (
	ErrEmptyNodeID            = configError("node ID is required")
	ErrEmptyChainID           = configError("chain ID is required")
	ErrEmptyListenAddr        = configError("listen address is required")
	ErrInsufficientValidators = configError("at least 4 validators are required for BFT")
)
