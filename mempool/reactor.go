// Package mempool provides a transaction mempool for the PBFT consensus engine.
package mempool

import (
	"context"
	"fmt"
	"sync"
	"time"
)

/*
================================================================================
                         MEMPOOL REACTOR
================================================================================

Reactor는 Mempool과 네트워크 레이어를 연결합니다.
P2P 네트워크를 통해 트랜잭션을 전파하고 수신합니다.

┌─────────────────────────────────────────────────────────────────────────────┐
│                              REACTOR                                         │
│                                                                              │
│   ┌───────────┐         ┌───────────┐         ┌───────────┐                │
│   │   Client  │         │  Reactor  │         │   Peers   │                │
│   └─────┬─────┘         └─────┬─────┘         └─────┬─────┘                │
│         │                     │                     │                       │
│         │  SubmitTx           │                     │                       │
│         │────────────────────►│                     │                       │
│         │                     │                     │                       │
│         │                     │  AddTx to Mempool   │                       │
│         │                     │────────┐            │                       │
│         │                     │        │            │                       │
│         │                     │◄───────┘            │                       │
│         │                     │                     │                       │
│         │                     │  BroadcastTx        │                       │
│         │                     │────────────────────►│                       │
│         │                     │                     │                       │
│         │                     │◄────────────────────│                       │
│         │                     │  ReceiveTx          │                       │
│         │                     │                     │                       │
│         │                     │  AddTx to Mempool   │                       │
│         │                     │────────┐            │                       │
│         │                     │        │            │                       │
│         │                     │◄───────┘            │                       │
│         │                     │                     │                       │
│   └─────┴─────┘         └─────┴─────┘         └─────┴─────┘                │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘

================================================================================
*/

// Broadcaster는 트랜잭션 전파를 위한 인터페이스임.
type Broadcaster interface {
	// 모든 피어에 대한 트랜잭션을 전파함
	BroadcastTx(tx []byte) error

	// SendTx는 특정 피어에 대한 트랜잭션 전파임
	SendTx(peerID string, tx []byte) error
}

// ReactorConfig는 리액터 설정임.
type ReactorConfig struct {
	// 브로드캐스트 설정
	BroadcastEnabled bool          // 브로드캐스트 활성화
	BroadcastDelay   time.Duration // 브로드캐스트 지연 (배치용)
	MaxBroadcastBatch int          // 한 번에 브로드캐스트할 최대 tx 수

	// 수신 설정
	MaxPendingTxs int // 처리 대기 최대 tx 수
}

// DefaultReactorConfig는 리액터의 리폴트 설정값임
func DefaultReactorConfig() *ReactorConfig {
	return &ReactorConfig{
		BroadcastEnabled:  true,
		BroadcastDelay:    10 * time.Millisecond,
		MaxBroadcastBatch: 100,
		MaxPendingTxs:     10000,
	}
}

// Reactor는 네트워크 계층과 맴풀을 연결함
type Reactor struct {
	mu sync.RWMutex

	config  *ReactorConfig
	mempool *Mempool

	// 네트워크 브로드캐스터
	broadcaster Broadcaster 

	// 브로드캐스트 큐
	broadcastQueue chan *Tx

	// 상태
	isRunning bool

	// 컨텍스트
	ctx    context.Context
	cancel context.CancelFunc
}

// NewReactor creates a new mempool reactor.
func NewReactor(mempool *Mempool, config *ReactorConfig) *Reactor {
	if config == nil {
		config = DefaultReactorConfig()
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &Reactor{
		config:         config,
		mempool:        mempool,
		broadcastQueue: make(chan *Tx, config.MaxPendingTxs),
		ctx:            ctx,
		cancel:         cancel,
	}
}

// SetBroadcaster sets the network broadcaster.
func (r *Reactor) SetBroadcaster(b Broadcaster) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.broadcaster = b
}

// Start starts the reactor.
func (r *Reactor) Start() error {
	r.mu.Lock()
	if r.isRunning {
		r.mu.Unlock()
		return nil
	}
	r.isRunning = true
	r.mu.Unlock()

	// 새 트랜잭션 수신 및 브로드캐스트 고루틴
	go r.broadcastLoop()

	// 멤풀의 새 트랜잭션 채널 수신
	go r.receiveNewTxLoop()

	return nil
}

// Stop stops the reactor.
func (r *Reactor) Stop() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.isRunning {
		return nil
	}

	r.isRunning = false
	r.cancel()

	return nil
}

/*
================================================================================
                      트랜잭션 제출 (클라이언트 → 멤풀)
================================================================================

  Client        Reactor           Mempool           Network
    │              │                 │                 │
    │ SubmitTx     │                 │                 │
    │ ────────────►│                 │                 │
    │              │                 │                 │
    │              │   AddTx         │                 │
    │              │ ───────────────►│                 │
    │              │                 │                 │
    │              │◄────────────────│                 │
    │              │   결과          │                 │
    │              │                 │                 │
    │              │   BroadcastTx   │                 │
    │              │ ────────────────┼────────────────►│
    │              │                 │                 │
    │◄─────────────│                 │                 │
    │   결과       │                 │                 │

================================================================================
*/

// SubmitTx submits a transaction to the mempool and broadcasts it.
func (r *Reactor) SubmitTx(txBytes []byte) error {
	return r.SubmitTxWithMeta(txBytes, "", 0, 0, 0)
}

// SubmitTxWithMeta submits a transaction with metadata.
func (r *Reactor) SubmitTxWithMeta(txBytes []byte, sender string, nonce, gasPrice, gasLimit uint64) error {
	// 멤풀에 추가
	if err := r.mempool.AddTxWithMeta(txBytes, sender, nonce, gasPrice, gasLimit); err != nil {
		return err
	}

	// 브로드캐스트 큐에 추가
	if r.config.BroadcastEnabled {
		tx := NewTxWithMeta(txBytes, sender, nonce, gasPrice, gasLimit)
		select {
		case r.broadcastQueue <- tx:
		default:
			// 큐가 가득 차면 무시 (이미 멤풀에는 추가됨)
		}
	}

	return nil
}

/*
================================================================================
                    트랜잭션 수신 (피어 → 멤풀)
================================================================================

  Peer          Reactor           Mempool
    │              │                 │
    │ ReceiveTx    │                 │
    │ ────────────►│                 │
    │              │                 │
    │              │   AddTx         │
    │              │ ───────────────►│
    │              │                 │
    │              │◄────────────────│
    │              │   결과          │
    │              │                 │
    │              │ (이미 있으면    │
    │              │  브로드캐스트X) │
    │              │                 │

================================================================================
*/

// ReceiveTx handles a transaction received from a peer.
func (r *Reactor) ReceiveTx(peerID string, txBytes []byte) error {
	// 멤풀에 추가 (브로드캐스트 하지 않음 - 이미 네트워크에 있음)
	err := r.mempool.AddTx(txBytes)
	if err != nil {
		// ErrTxAlreadyExists는 정상 (다른 피어에서도 받았을 수 있음)
		if err == ErrTxAlreadyExists {
			return nil
		}
		return err
	}

	return nil
}

// broadcastLoop handles broadcasting transactions to peers.
func (r *Reactor) broadcastLoop() {
	var batch []*Tx
	ticker := time.NewTicker(r.config.BroadcastDelay)
	defer ticker.Stop()

	for {
		select {
		case <-r.ctx.Done():
			return

		case tx := <-r.broadcastQueue:
			batch = append(batch, tx)

			// 배치가 가득 차면 즉시 전송
			if len(batch) >= r.config.MaxBroadcastBatch {
				r.broadcastBatch(batch)
				batch = nil
			}

		case <-ticker.C:
			// 주기적으로 배치 전송
			if len(batch) > 0 {
				r.broadcastBatch(batch)
				batch = nil
			}
		}
	}
}

// broadcastBatch broadcasts a batch of transactions.
func (r *Reactor) broadcastBatch(batch []*Tx) {
	r.mu.RLock()
	broadcaster := r.broadcaster
	r.mu.RUnlock()

	if broadcaster == nil {
		return
	}

	for _, tx := range batch {
		if err := broadcaster.BroadcastTx(tx.Data); err != nil {
			// 브로드캐스트 실패 로그
			fmt.Printf("[Reactor] Failed to broadcast tx %s: %v\n", tx.ID[:8], err)
		}
	}
}

// receiveNewTxLoop listens for new transactions from the mempool.
func (r *Reactor) receiveNewTxLoop() {
	for {
		select {
		case <-r.ctx.Done():
			return
		case tx, ok := <-r.mempool.NewTxCh():
			if !ok {
				return
			}
			// 새 트랜잭션이 멤풀에 추가되면 브로드캐스트
			// (SubmitTx를 통해 추가된 경우 이미 큐에 있음)
			_ = tx // 필요시 추가 처리
		}
	}
}

// GetMempool returns the underlying mempool.
func (r *Reactor) GetMempool() *Mempool {
	return r.mempool
}
