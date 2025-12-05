// Package pbft provides PBFT consensus engine interface.
package pbft

import (
	"github.com/ahwlsqja/pbft-cosmos/mempool"
	"github.com/ahwlsqja/pbft-cosmos/types"
)

// ConsensusEngine defines the interface for PBFT consensus engines.
// Engine과 EngineV2 모두 이 인터페이스를 구현합니다.
type ConsensusEngine interface {
	// Lifecycle
	Start() error
	Stop()

	// Mempool
	SetMempool(mp *mempool.Mempool)
	GetMempool() *mempool.Mempool

	// Primary/Leader
	IsPrimary() bool

	// Request submission
	SubmitRequest(operation []byte, clientID string) error

	// State queries
	GetCurrentView() uint64
	GetCurrentHeight() uint64
	GetCommittedBlocks() []*types.Block

	// StateProvider interface methods
	GetBlocksFromHeight(fromHeight uint64) []*types.Block
	GetCheckpoints() []Checkpoint
	GetCheckpoint(seqNum uint64) (*Checkpoint, bool)
}
