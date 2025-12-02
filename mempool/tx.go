// Package mempool provides a transaction mempool for the PBFT consensus engine.
package mempool

import (
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// Tx represents a transaction in the mempool.
type Tx struct {
	// 트랜잭션 식별자
	Hash []byte // SHA256 해시
	ID   string // 해시의 hex 문자열

	// 트랜잭션 데이터
	Data []byte // 원본 트랜잭션 바이트

	// 메타데이터
	Sender    string    // 발신자 주소
	Nonce     uint64    // 발신자별 순차 번호
	GasPrice  uint64    // 가스 가격 (우선순위 결정용)
	GasLimit  uint64    // 가스 한도
	Timestamp time.Time // 멤풀 진입 시간

	// 상태
	Height    int64 // CheckTx 시점의 블록 높이
	CheckedAt time.Time
}

// NewTx creates a new transaction from raw bytes.
func NewTx(data []byte) *Tx {
	hash := sha256.Sum256(data)
	return &Tx{
		Hash:      hash[:],
		ID:        hex.EncodeToString(hash[:]),
		Data:      data,
		Timestamp: time.Now(),
		CheckedAt: time.Now(),
	}
}

// NewTxWithMeta creates a new transaction with metadata.
func NewTxWithMeta(data []byte, sender string, nonce, gasPrice, gasLimit uint64) *Tx {
	tx := NewTx(data)
	tx.Sender = sender
	tx.Nonce = nonce
	tx.GasPrice = gasPrice
	tx.GasLimit = gasLimit
	return tx
}

// Size returns the size of the transaction in bytes.
func (tx *Tx) Size() int {
	return len(tx.Data)
}

// Age returns how long the transaction has been in the mempool.
func (tx *Tx) Age() time.Duration {
	return time.Since(tx.Timestamp)
}

// Key returns the unique key for this transaction.
func (tx *Tx) Key() string {
	return tx.ID
}

// SenderKey returns the key for sender-based lookups.
func (tx *Tx) SenderKey() string {
	return tx.Sender
}

// Priority returns the priority score for ordering.
// Higher gas price = higher priority.
func (tx *Tx) Priority() uint64 {
	return tx.GasPrice
}
