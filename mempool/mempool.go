// Package mempool provides a transaction mempool for the PBFT consensus engine.
package mempool

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

/*
================================================================================
                           MEMPOOL 아키텍처
================================================================================

ABCI 2.0 설계 철학:
- Mempool: FIFO 순서로 트랜잭션 저장 (단순성, 빠른 삽입/삭제)
- PrepareProposal: ABCI 앱에서 트랜잭션 정렬/필터링 담당 (유연성)

┌─────────────────────────────────────────────────────────────────────────────┐
│                              MEMPOOL (FIFO)                                  │
│                                                                              │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │                         txStore (map)                                │    │
│  │                    [txHash] -> *Tx                                   │    │
│  │   ┌────┐ ┌────┐ ┌────┐ ┌────┐ ┌────┐ ┌────┐ ┌────┐ ┌────┐          │    │
│  │   │tx1 │ │tx2 │ │tx3 │ │tx4 │ │tx5 │ │tx6 │ │tx7 │ │... │          │    │
│  │   └────┘ └────┘ └────┘ └────┘ └────┘ └────┘ └────┘ └────┘          │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                                                                              │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │                      senderIndex (map)                               │    │
│  │                  [sender] -> []*Tx (nonce 정렬)                      │    │
│  │   ┌──────────────────┐  ┌──────────────────┐                        │    │
│  │   │ sender_A:        │  │ sender_B:        │                        │    │
│  │   │  [tx1, tx3, tx5] │  │  [tx2, tx4]      │                        │    │
│  │   └──────────────────┘  └──────────────────┘                        │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                                                                              │
│  ReapMaxTxs() → FIFO 순서 (Timestamp)로 반환                                 │
│  정렬은 ABCI App의 PrepareProposal에서 처리                                   │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘

================================================================================
*/

var (
	// 에러 정의
	ErrTxAlreadyExists   = errors.New("transaction already exists in mempool")
	ErrMempoolFull       = errors.New("mempool is full")
	ErrTxTooLarge        = errors.New("transaction too large")
	ErrTxExpired         = errors.New("transaction expired")
	ErrInvalidTx         = errors.New("invalid transaction")
	ErrLowNonce          = errors.New("nonce too low")
	ErrNonceGap          = errors.New("nonce gap detected")
	ErrInsufficientGas   = errors.New("insufficient gas price")
	ErrMempoolNotRunning = errors.New("mempool is not running")
)

// 맴풀 설정 파일 
type Config struct {
	// 크기 제한
	MaxTxs      int   // 최대 트랜잭션 수 (기본: 5000)
	MaxBytes    int64 // 최대 바이트 (기본: 1GB)
	MaxTxBytes  int   // 단일 트랜잭션 최대 바이트 (기본: 1MB)
	MaxBatchTxs int   // 한 번에 가져올 최대 트랜잭션 수 (기본: 500)

	// TTL (Time To Live)
	TTL time.Duration // 트랜잭션 만료 시간 (기본: 10분)

	// 재검사
	RecheckEnabled bool          // 블록 후 재검사 활성화
	RecheckTimeout time.Duration // 재검사 타임아웃

	// 캐시
	CacheSize int // 최근 제거된 tx 캐시 크기

	// 최소 가스 가격
	MinGasPrice uint64
}

// 디폴트 맴풀 설정
func DefaultConfig() *Config {
	return &Config{
		MaxTxs:         5000,
		MaxBytes:       1024 * 1024 * 1024, // 1GB
		MaxTxBytes:     1024 * 1024,        // 1MB
		MaxBatchTxs:    500,
		TTL:            10 * time.Minute,
		RecheckEnabled: true,
		RecheckTimeout: 5 * time.Second,
		CacheSize:      10000,
		MinGasPrice:    0,
	}
}

// 트랜잭션 검증하는 콜백 함수 정의
// 검증되면 nil 반환 그게 아니면 에러 리턴
type CheckTxCallback func(tx *Tx) error

// 맴풀이 대기중인 트랜잭션을 관리함
type Mempool struct {
	mu sync.RWMutex

	// 설정
	config *Config

	// 트랜잭션 저장소
	txStore map[string]*Tx // txHash -> Tx

	// 발신자별 인덱스 (nonce 순서 유지)
	senderIndex map[string][]*Tx // sender -> []*Tx (nonce 정렬)

	// 현재 상태
	txCount   int   // 현재 트랜잭션 수
	txBytes   int64 // 현재 총 바이트
	height    int64 // 현재 블록 높이
	isRunning bool

	// 발신자별 마지막 nonce 추적
	senderNonce map[string]uint64 // sender -> lastNonce

	// 최근 제거된 트랜잭션 캐시 (중복 방지)
	recentlyRemoved map[string]time.Time

	// 콜백
	checkTxCallback CheckTxCallback

	// 브로드캐스트 채널
	newTxCh chan *Tx

	// 종료
	ctx    context.Context
	cancel context.CancelFunc

	// 메트릭
	metrics *MempoolMetrics
}

// 맴풀 메트릭
type MempoolMetrics struct {
	mu sync.RWMutex

	TxsReceived   int64 // 받은 총 트랜잭션 수
	TxsAccepted   int64 // 수락된 트랜잭션 수
	TxsRejected   int64 // 거부된 트랜잭션 수
	TxsExpired    int64 // 만료된 트랜잭션 수
	TxsEvicted    int64 // 퇴출된 트랜잭션 수
	TxsCommitted  int64 // 커밋된 트랜잭션 수
	RecheckCount  int64 // 재검사 횟수
	CurrentSize   int   // 현재 크기
	CurrentBytes  int64 // 현재 바이트
	PeakSize      int   // 최대 크기
	PeakBytes     int64 // 최대 바이트
	LastBlockTime time.Time // 마지막 블록 시간
}

// 새로운 맴풀 생성
func NewMempool(config *Config) *Mempool {
	if config == nil {
		config = DefaultConfig()
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &Mempool{
		config:          config,
		txStore:         make(map[string]*Tx),
		senderIndex:     make(map[string][]*Tx),
		senderNonce:     make(map[string]uint64),
		recentlyRemoved: make(map[string]time.Time),
		newTxCh:         make(chan *Tx, 1000),
		ctx:             ctx,
		cancel:          cancel,
		metrics:         &MempoolMetrics{},
	}
}

// Start starts the mempool background processes.
func (mp *Mempool) Start() error {
	mp.mu.Lock()
	if mp.isRunning {
		mp.mu.Unlock()
		return nil
	}
	mp.isRunning = true
	mp.mu.Unlock()

	// 만료 트랜잭션 정리 고루틴
	go mp.expireLoop()

	// 캐시 정리 고루틴
	go mp.cleanupCacheLoop()

	return nil
}

// Stop stops the mempool.
func (mp *Mempool) Stop() error {
	mp.mu.Lock()
	defer mp.mu.Unlock()

	if !mp.isRunning {
		return nil
	}

	mp.isRunning = false
	mp.cancel()
	close(mp.newTxCh)

	return nil
}

// SetCheckTxCallback sets the transaction validation callback.
func (mp *Mempool) SetCheckTxCallback(cb CheckTxCallback) {
	mp.mu.Lock()
	defer mp.mu.Unlock()
	mp.checkTxCallback = cb
}

/*
================================================================================
                          트랜잭션 추가 흐름
================================================================================

  Client                   Mempool                    ABCI App
    │                         │                          │
    │   1. AddTx(txBytes)     │                          │
    │ ───────────────────────►│                          │
    │                         │                          │
    │                         │ 2. 중복 체크              │
    │                         │    (txStore에 존재?)      │
    │                         │                          │
    │                         │ 3. 크기 체크              │
    │                         │    (MaxTxBytes)          │
    │                         │                          │
    │                         │ 4. CheckTx 콜백          │
    │                         │ ────────────────────────►│
    │                         │                          │
    │                         │◄────────────────────────│
    │                         │    5. 검증 결과          │
    │                         │                          │
    │                         │ 6. 용량 체크             │
    │                         │    (MaxTxs, MaxBytes)    │
    │                         │                          │
    │                         │ 7. 필요시 퇴출           │
    │                         │    (낮은 가스 우선)      │
    │                         │                          │
    │                         │ 8. 저장                  │
    │                         │    - txStore             │
    │                         │    - senderIndex         │
    │                         │                          │
    │  9. 결과 반환           │                          │
    │◄────────────────────── │                          │
    │                         │                          │

================================================================================
*/

// AddTx adds a transaction to the mempool.
func (mp *Mempool) AddTx(txBytes []byte) error {
	return mp.AddTxWithMeta(txBytes, "", 0, 0, 0)
}

// 메타데이터와 함께 트랜잭션 추가. 이미 맴풀이 init 되었기 때문에
//  현재는 MVP/테스트 단계라서 단순화된 것이고, ABCI 앱과 연동 시 앱이 CheckTx에서 이 검증들을 수행합니다. 즉, Mempool은 단순 저장소 역할이고 실제 검증은 ABCI 앱이 담당하는 ABCI
// 2.0 설계 철학을 따른 것입니다. 
func (mp *Mempool) AddTxWithMeta(txBytes []byte, sender string, nonce, gasPrice, gasLimit uint64) error {
	// 1. 일단 Mempool 접근 쓰레드에 락걸고
	mp.mu.Lock()
	// 2. 끝날 때 락풀음
	defer mp.mu.Unlock()

	// 3. 맴풀이 실행 중이 아닐 때 에러 리턴
	if !mp.isRunning {
		return ErrMempoolNotRunning
	}

	// 메트릭 업데이트
	// 4-1 매트릭 락걸고
	mp.metrics.mu.Lock()
	// 4-2 트랜잭션 받은거 ++ 하고
	mp.metrics.TxsReceived++
	// 4-3 락 풀고
	mp.metrics.mu.Unlock()

	// 5. 설정에 해놨던 최대 트랜잭션 바이트 크기 체크
	if len(txBytes) > mp.config.MaxTxBytes {
		// 크기를 넘어가면 트랜잭션 리젝함
		mp.rejectTx()
		return fmt.Errorf("%w: size %d > max %d", ErrTxTooLarge, len(txBytes), mp.config.MaxTxBytes)
	}

	// 6. 트랜잭션 생성 tx.go line[44]에 있음
	// 그냥 tx 데이터 자체를 한번 해시하고 그 나머지 데이터들 메타데이터로 넣어서 tx 객체 만든거임
	tx := NewTxWithMeta(txBytes, sender, nonce, gasPrice, gasLimit)
	// 맴풀에 있는 블록 높이 꺼내서 tx.Height에 저장함
	tx.Height = mp.height

	// 7. 중복 체크
	// 현재 txStore (맴풀 구조체에서 txHash: 데이터 이런 데이터 형식으로 있는 자료 구조에서 중복되는 키가 존재하면)
	// 에러 rejectTx 발생함 -> ErrTxAlreadyExists
	if _, exists := mp.txStore[tx.ID]; exists {
		mp.rejectTx()
		return ErrTxAlreadyExists
		// mempool.go 에 line[52]
	}

	// 8. 최근 제거된 트랜잭션 체크
	if _, removed := mp.recentlyRemoved[tx.ID]; removed {
		mp.rejectTx()
		return ErrTxAlreadyExists
	}

	// 9. 최소 가스 가격 체크 설정해둔 MinGasPrice 보다 작으면 rejectTx
	if gasPrice < mp.config.MinGasPrice {
		mp.rejectTx()
		return fmt.Errorf("%w: price %d < min %d", ErrInsufficientGas, gasPrice, mp.config.MinGasPrice)
	}

	// 10. Nonce 체크 (sender가 있는 경우) line[360]에 있음
	if sender != "" {
		if err := mp.checkNonce(sender, nonce); err != nil {
			mp.rejectTx()
			return err
		}
	}

	// 7. CheckTx 콜백 호출
	if mp.checkTxCallback != nil {
		if err := mp.checkTxCallback(tx); err != nil {
			mp.rejectTx()
			return fmt.Errorf("%w: %v", ErrInvalidTx, err)
		}
	}

	// 8. 용량 체크 및 필요시 퇴출
	if err := mp.ensureCapacity(tx); err != nil {
		mp.rejectTx()
		return err
	}

	// 9. 저장
	mp.addTxLocked(tx)

	// 10. 새 트랜잭션 알림 (브로드캐스트용)
	select {
	case mp.newTxCh <- tx:
	default:
		// 채널이 가득 차면 무시
	}

	mp.acceptTx()
	return nil
}

// checkNonce는 트랜잭션 nonce를 검증하는 메서드임(샌더랑 논스 받음)
func (mp *Mempool) checkNonce(sender string, nonce uint64) error {
	// 1. senderNonce에 sender 키가 없으면 !exist 이고 첫 트랜잭션임.
	// 따라서 nonce가 0이므로 return nil
	lastNonce, exists := mp.senderNonce[sender]
	if !exists {
		// 첫 트랜잭션
		return nil
	}

	// 2. 받은 nonce가 lastNonce 보다 작거나 같으면 에러. 왜냐하면 nonce 마지막 nonce보다 작으면 그건 말이안됨
	if nonce <= lastNonce {
		return fmt.Errorf("%w: got %d, expected > %d", ErrLowNonce, nonce, lastNonce)
	}

	if nonce > lastNonce+1 {
		// Nonce 갭 허용 (pending 트랜잭션 고려)
		// 엄격한 모드에서는 에러 반환
		// return fmt.Errorf("%w: got %d, expected %d", ErrNonceGap, nonce, lastNonce+1)
	}

	return nil
}

// ensureCapacity ensures there's room for the new transaction.
func (mp *Mempool) ensureCapacity(newTx *Tx) error {
	// 트랜잭션 수 체크
	for mp.txCount >= mp.config.MaxTxs {
		if err := mp.evictLowestPriority(newTx.GasPrice); err != nil {
			return ErrMempoolFull
		}
	}

	// 바이트 체크
	for mp.txBytes+int64(newTx.Size()) > mp.config.MaxBytes {
		if err := mp.evictLowestPriority(newTx.GasPrice); err != nil {
			return ErrMempoolFull
		}
	}

	return nil
}

// evictLowestPriority removes the lowest priority transaction.
func (mp *Mempool) evictLowestPriority(minPrice uint64) error {
	var lowestTx *Tx
	var lowestPrice uint64 = ^uint64(0) // Max uint64

	for _, tx := range mp.txStore {
		if tx.GasPrice < lowestPrice {
			lowestPrice = tx.GasPrice
			lowestTx = tx
		}
	}

	if lowestTx == nil {
		return errors.New("no transaction to evict")
	}

	// 새 트랜잭션보다 낮은 우선순위만 퇴출
	if lowestPrice >= minPrice {
		return errors.New("cannot evict higher priority transaction")
	}

	mp.removeTxLocked(lowestTx.ID, true)

	mp.metrics.mu.Lock()
	mp.metrics.TxsEvicted++
	mp.metrics.mu.Unlock()

	return nil
}

// addTxLocked adds a transaction (must hold lock).
func (mp *Mempool) addTxLocked(tx *Tx) {
	// txStore에 추가
	mp.txStore[tx.ID] = tx
	mp.txCount++
	mp.txBytes += int64(tx.Size())

	// senderIndex에 추가
	if tx.Sender != "" {
		senderTxs := mp.senderIndex[tx.Sender]
		senderTxs = append(senderTxs, tx)
		// Nonce 순서로 정렬
		sort.Slice(senderTxs, func(i, j int) bool {
			return senderTxs[i].Nonce < senderTxs[j].Nonce
		})
		mp.senderIndex[tx.Sender] = senderTxs

		// Nonce 업데이트
		if tx.Nonce > mp.senderNonce[tx.Sender] {
			mp.senderNonce[tx.Sender] = tx.Nonce
		}
	}

	// 메트릭 업데이트
	mp.updateMetrics()
}

// removeTxLocked removes a transaction (must hold lock).
func (mp *Mempool) removeTxLocked(txID string, addToCache bool) {
	tx, exists := mp.txStore[txID]
	if !exists {
		return
	}

	// txStore에서 제거
	delete(mp.txStore, txID)
	mp.txCount--
	mp.txBytes -= int64(tx.Size())

	// senderIndex에서 제거
	if tx.Sender != "" {
		senderTxs := mp.senderIndex[tx.Sender]
		for i, t := range senderTxs {
			if t.ID == txID {
				mp.senderIndex[tx.Sender] = append(senderTxs[:i], senderTxs[i+1:]...)
				break
			}
		}
		if len(mp.senderIndex[tx.Sender]) == 0 {
			delete(mp.senderIndex, tx.Sender)
		}
	}

	// 캐시에 추가
	if addToCache {
		mp.recentlyRemoved[txID] = time.Now()
	}

	mp.updateMetrics()
}

/*
================================================================================
                       트랜잭션 조회 (블록 제안용)
================================================================================

  ABCI 2.0 설계 철학:
  - Mempool: FIFO 순서로 트랜잭션 저장/반환 (단순성, O(1) 삽입/삭제)
  - PrepareProposal: ABCI 앱에서 트랜잭션 정렬/필터링 담당 (유연성)

  Primary Node              Mempool                    ABCI App
       │                       │                          │
       │  1. ReapMaxTxs(max)   │                          │
       │ ─────────────────────►│                          │
       │                       │                          │
       │                       │ 2. FIFO 순서로 반환       │
       │                       │    (정렬 없음)            │
       │                       │                          │
       │  3. []*Tx 반환        │                          │
       │◄───────────────────── │                          │
       │                       │                          │
       │  4. PrepareProposal(txs)                         │
       │ ────────────────────────────────────────────────►│
       │                       │                          │
       │                       │     5. 앱에서 정렬/필터링  │
       │                       │        (GasPrice, MEV 등) │
       │                       │                          │
       │  6. 정렬된 txs 반환   │                          │
       │◄──────────────────────────────────────────────── │
       │                       │                          │

================================================================================
*/

// ReapMaxTxs returns up to max transactions for block proposal.
// Returns transactions in FIFO order (arrival time).
// Sorting/filtering should be done by the ABCI app in PrepareProposal.
func (mp *Mempool) ReapMaxTxs(max int) []*Tx {
	mp.mu.RLock()
	defer mp.mu.RUnlock()

	if max <= 0 || max > mp.config.MaxBatchTxs {
		max = mp.config.MaxBatchTxs
	}

	if mp.txCount == 0 {
		return nil
	}

	// FIFO 순서로 트랜잭션 반환 (Timestamp 기준)
	txs := make([]*Tx, 0, mp.txCount)
	for _, tx := range mp.txStore {
		txs = append(txs, tx)
	}

	// 도착 순서(Timestamp)로 정렬 - FIFO
	sort.Slice(txs, func(i, j int) bool {
		return txs[i].Timestamp.Before(txs[j].Timestamp)
	})

	// 상위 max개 반환
	if len(txs) > max {
		txs = txs[:max]
	}

	return txs
}

// ReapMaxBytes returns transactions up to maxBytes in FIFO order.
// Sorting/filtering should be done by the ABCI app in PrepareProposal.
func (mp *Mempool) ReapMaxBytes(maxBytes int64) []*Tx {
	mp.mu.RLock()
	defer mp.mu.RUnlock()

	if mp.txCount == 0 {
		return nil
	}

	// FIFO 순서로 트랜잭션 가져오기
	allTxs := mp.ReapMaxTxs(mp.txCount)

	var result []*Tx
	var totalBytes int64

	for _, tx := range allTxs {
		if totalBytes+int64(tx.Size()) > maxBytes {
			break
		}
		result = append(result, tx)
		totalBytes += int64(tx.Size())
	}

	return result
}

/*
================================================================================
                         블록 커밋 후 처리
================================================================================

  Consensus Engine            Mempool
       │                         │
       │  1. Update(height,      │
       │     committedTxs)       │
       │ ───────────────────────►│
       │                         │
       │                         │ 2. 커밋된 tx 제거
       │                         │    (txStore, senderIndex)
       │                         │
       │                         │ 3. 높이 업데이트
       │                         │
       │                         │ 4. Recheck (선택적)
       │                         │    - 남은 tx 재검증
       │                         │    - 무효 tx 제거
       │                         │
       │  5. 완료                 │
       │◄─────────────────────── │
       │                         │

================================================================================
*/

// Update is called after a block is committed.
// It removes committed transactions and optionally rechecks remaining ones.
func (mp *Mempool) Update(height int64, committedTxs [][]byte) error {
	mp.mu.Lock()
	defer mp.mu.Unlock()

	mp.height = height
	mp.metrics.mu.Lock()
	mp.metrics.LastBlockTime = time.Now()
	mp.metrics.mu.Unlock()

	// 커밋된 트랜잭션 제거
	for _, txBytes := range committedTxs {
		tx := NewTx(txBytes)
		mp.removeTxLocked(tx.ID, true)

		mp.metrics.mu.Lock()
		mp.metrics.TxsCommitted++
		mp.metrics.mu.Unlock()
	}

	// Recheck (선택적)
	if mp.config.RecheckEnabled {
		mp.recheckTxsLocked()
	}

	return nil
}

// recheckTxsLocked rechecks all remaining transactions.
func (mp *Mempool) recheckTxsLocked() {
	if mp.checkTxCallback == nil {
		return
	}

	mp.metrics.mu.Lock()
	mp.metrics.RecheckCount++
	mp.metrics.mu.Unlock()

	toRemove := make([]string, 0)

	for id, tx := range mp.txStore {
		// 재검증
		if err := mp.checkTxCallback(tx); err != nil {
			toRemove = append(toRemove, id)
		}
	}

	// 무효 트랜잭션 제거
	for _, id := range toRemove {
		mp.removeTxLocked(id, false)
	}
}

/*
================================================================================
                            조회 메서드
================================================================================
*/

// GetTx returns a transaction by hash.
func (mp *Mempool) GetTx(txID string) *Tx {
	mp.mu.RLock()
	defer mp.mu.RUnlock()
	return mp.txStore[txID]
}

// HasTx checks if a transaction exists.
func (mp *Mempool) HasTx(txID string) bool {
	mp.mu.RLock()
	defer mp.mu.RUnlock()
	_, exists := mp.txStore[txID]
	return exists
}

// Size returns the current number of transactions.
func (mp *Mempool) Size() int {
	mp.mu.RLock()
	defer mp.mu.RUnlock()
	return mp.txCount
}

// SizeBytes returns the current total bytes.
func (mp *Mempool) SizeBytes() int64 {
	mp.mu.RLock()
	defer mp.mu.RUnlock()
	return mp.txBytes
}

// GetTxsBySender returns all transactions from a sender.
func (mp *Mempool) GetTxsBySender(sender string) []*Tx {
	mp.mu.RLock()
	defer mp.mu.RUnlock()

	txs := mp.senderIndex[sender]
	if txs == nil {
		return nil
	}

	result := make([]*Tx, len(txs))
	copy(result, txs)
	return result
}

// GetMetrics returns mempool metrics.
func (mp *Mempool) GetMetrics() MempoolMetrics {
	mp.metrics.mu.RLock()
	defer mp.metrics.mu.RUnlock()

	metrics := *mp.metrics
	mp.mu.RLock()
	metrics.CurrentSize = mp.txCount
	metrics.CurrentBytes = mp.txBytes
	mp.mu.RUnlock()

	return metrics
}

// NewTxCh returns the channel for new transaction notifications.
func (mp *Mempool) NewTxCh() <-chan *Tx {
	return mp.newTxCh
}

/*
================================================================================
                          백그라운드 작업
================================================================================
*/

// expireLoop periodically removes expired transactions.
func (mp *Mempool) expireLoop() {
	ticker := time.NewTicker(mp.config.TTL / 2)
	defer ticker.Stop()

	for {
		select {
		case <-mp.ctx.Done():
			return
		case <-ticker.C:
			mp.expireTxs()
		}
	}
}

// expireTxs removes expired transactions.
func (mp *Mempool) expireTxs() {
	mp.mu.Lock()
	defer mp.mu.Unlock()

	now := time.Now()
	toRemove := make([]string, 0)

	for id, tx := range mp.txStore {
		if now.Sub(tx.Timestamp) > mp.config.TTL {
			toRemove = append(toRemove, id)
		}
	}

	for _, id := range toRemove {
		mp.removeTxLocked(id, true)

		mp.metrics.mu.Lock()
		mp.metrics.TxsExpired++
		mp.metrics.mu.Unlock()
	}
}

// cleanupCacheLoop periodically cleans up the recently removed cache.
func (mp *Mempool) cleanupCacheLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-mp.ctx.Done():
			return
		case <-ticker.C:
			mp.cleanupCache()
		}
	}
}

// cleanupCache removes old entries from the recently removed cache.
func (mp *Mempool) cleanupCache() {
	mp.mu.Lock()
	defer mp.mu.Unlock()

	cutoff := time.Now().Add(-mp.config.TTL)

	for id, removedAt := range mp.recentlyRemoved {
		if removedAt.Before(cutoff) {
			delete(mp.recentlyRemoved, id)
		}
	}

	// 캐시 크기 제한
	if len(mp.recentlyRemoved) > mp.config.CacheSize {
		// 가장 오래된 것들 제거
		type entry struct {
			id   string
			time time.Time
		}
		entries := make([]entry, 0, len(mp.recentlyRemoved))
		for id, t := range mp.recentlyRemoved {
			entries = append(entries, entry{id, t})
		}
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].time.Before(entries[j].time)
		})

		toRemove := len(mp.recentlyRemoved) - mp.config.CacheSize
		for i := 0; i < toRemove; i++ {
			delete(mp.recentlyRemoved, entries[i].id)
		}
	}
}

// Flush removes all transactions from the mempool.
func (mp *Mempool) Flush() {
	mp.mu.Lock()
	defer mp.mu.Unlock()

	mp.txStore = make(map[string]*Tx)
	mp.senderIndex = make(map[string][]*Tx)
	mp.senderNonce = make(map[string]uint64)
	mp.txCount = 0
	mp.txBytes = 0

	mp.updateMetrics()
}

/*
================================================================================
                              헬퍼 메서드
================================================================================
*/

func (mp *Mempool) acceptTx() {
	mp.metrics.mu.Lock()
	mp.metrics.TxsAccepted++
	mp.metrics.mu.Unlock()
}

func (mp *Mempool) rejectTx() {
	mp.metrics.mu.Lock()
	mp.metrics.TxsRejected++
	mp.metrics.mu.Unlock()
}

func (mp *Mempool) updateMetrics() {
	mp.metrics.mu.Lock()
	defer mp.metrics.mu.Unlock()

	mp.metrics.CurrentSize = mp.txCount
	mp.metrics.CurrentBytes = mp.txBytes

	if mp.txCount > mp.metrics.PeakSize {
		mp.metrics.PeakSize = mp.txCount
	}
	if mp.txBytes > mp.metrics.PeakBytes {
		mp.metrics.PeakBytes = mp.txBytes
	}
}
