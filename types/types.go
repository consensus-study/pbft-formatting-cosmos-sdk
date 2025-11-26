// Package types defines core data structures for the PBFT consensus engine.
package types

import (
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// Block represents a block in the blockchain.
type Block struct {
	Header       BlockHeader   `json:"header"`
	Transactions []Transaction `json:"transactions"`
	Hash         []byte        `json:"hash"`
}

// BlockHeader contains metadata about the block.
type BlockHeader struct {
	Height       uint64    `json:"height"`
	PrevHash     []byte    `json:"prev_hash"`
	Timestamp    time.Time `json:"timestamp"`
	ProposerID   string    `json:"proposer_id"`
	StateRoot    []byte    `json:"state_root"`
	TxRoot       []byte    `json:"tx_root"`
	View         uint64    `json:"view"`
	SequenceNum  uint64    `json:"sequence_num"`
}

// Transaction represents a transaction in the blockchain.
type Transaction struct {
	ID        string    `json:"id"`
	Data      []byte    `json:"data"`
	Timestamp time.Time `json:"timestamp"`
	Signature []byte    `json:"signature"`
	From      string    `json:"from"`
}

// Validator represents a node participating in consensus.
type Validator struct {
	ID        string `json:"id"`
	Address   string `json:"address"`
	PublicKey []byte `json:"public_key"`
	Power     int64  `json:"power"`
}

// ValidatorSet represents the set of validators for consensus.
type ValidatorSet struct {
	Validators []*Validator `json:"validators"`
	Proposer   *Validator   `json:"proposer"`
}

// NewValidatorSet creates a new validator set.
func NewValidatorSet(validators []*Validator) *ValidatorSet {
	vs := &ValidatorSet{
		Validators: validators,
	}
	if len(validators) > 0 {
		vs.Proposer = validators[0]
	}
	return vs
}

// Size returns the number of validators.
func (vs *ValidatorSet) Size() int {
	return len(vs.Validators)
}

// GetByID returns a validator by ID.
func (vs *ValidatorSet) GetByID(id string) *Validator {
	for _, v := range vs.Validators {
		if v.ID == id {
			return v
		}
	}
	return nil
}

// QuorumSize returns the minimum number of nodes required for consensus (2f + 1).
func (vs *ValidatorSet) QuorumSize() int {
	n := len(vs.Validators)
	f := (n - 1) / 3 // Maximum faulty nodes
	return 2*f + 1
}

// FaultyTolerance returns the maximum number of faulty nodes (f).
func (vs *ValidatorSet) FaultyTolerance() int {
	return (len(vs.Validators) - 1) / 3
}

// ComputeHash computes the SHA256 hash of the block.
func (b *Block) ComputeHash() []byte {
	data := append(b.Header.PrevHash, []byte(b.Header.ProposerID)...)
	data = append(data, byte(b.Header.Height))
	data = append(data, byte(b.Header.View))
	for _, tx := range b.Transactions {
		data = append(data, tx.Data...)
	}
	hash := sha256.Sum256(data)
	return hash[:]
}

// HashString returns the hex-encoded hash string.
func (b *Block) HashString() string {
	return hex.EncodeToString(b.Hash)
}

// NewBlock creates a new block with the given parameters.
func NewBlock(height uint64, prevHash []byte, proposerID string, view uint64, txs []Transaction) *Block {
	block := &Block{
		Header: BlockHeader{
			Height:      height,
			PrevHash:    prevHash,
			Timestamp:   time.Now(),
			ProposerID:  proposerID,
			View:        view,
			SequenceNum: height,
		},
		Transactions: txs,
	}
	block.Hash = block.ComputeHash()
	return block
}
