# 트랜잭션 코드 플로우 분석

## 1. 트랜잭션 전체 생명주기

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                    TRANSACTION LIFECYCLE                                     │
│                                                                              │
│  ┌──────────┐    ┌──────────┐    ┌──────────┐    ┌──────────┐    ┌────────┐│
│  │ Creation │───►│ Mempool  │───►│ Proposal │───►│ Consensus│───►│Execute ││
│  └──────────┘    └──────────┘    └──────────┘    └──────────┘    └────────┘│
│       │               │               │               │               │      │
│       │               │               │               │               │      │
│       ▼               ▼               ▼               ▼               ▼      │
│   Client          CheckTx        PrepareProposal  PBFT Phases   FinalizeBlock│
│   Submit          Validation     Tx Ordering      Pre/Pre/Commit  Commit    │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

## 2. 트랜잭션 제출 플로우

### 2.1 클라이언트 제출 경로

```
┌─────────────────────────────────────────────────────────────────┐
│                TRANSACTION SUBMISSION                            │
│                                                                  │
│  Client         Node          Reactor        Mempool    ABCI    │
│     │            │              │              │          │      │
│     │ SubmitTx   │              │              │          │      │
│     │───────────►│              │              │          │      │
│     │            │              │              │          │      │
│     │            │ SubmitTx     │              │          │      │
│     │            │─────────────►│              │          │      │
│     │            │              │              │          │      │
│     │            │              │ AddTxWithMeta│          │      │
│     │            │              │─────────────►│          │      │
│     │            │              │              │          │      │
│     │            │              │              │ CheckTx  │      │
│     │            │              │              │─────────►│      │
│     │            │              │              │          │      │
│     │            │              │              │◄─────────│      │
│     │            │              │              │ Code=0   │      │
│     │            │              │              │          │      │
│     │            │              │◄─────────────│          │      │
│     │            │              │   success    │          │      │
│     │            │              │              │          │      │
│     │            │◄─────────────│              │          │      │
│     │            │              │              │          │      │
│     │◄───────────│              │              │          │      │
│     │  success   │              │              │          │      │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

**코드 경로:**
```go
// 1. node/node.go - SubmitTx()
func (n *Node) SubmitTx(ctx context.Context, txBytes []byte) error {
    return n.reactor.SubmitTx(txBytes)
}

// 2. mempool/reactor.go:187 - SubmitTx()
func (r *Reactor) SubmitTx(txBytes []byte) error {
    return r.SubmitTxWithMeta(txBytes, "", 0, 0, 0)
}

// 3. mempool/reactor.go:192 - SubmitTxWithMeta()
func (r *Reactor) SubmitTxWithMeta(txBytes []byte, sender string,
    nonce, gasPrice, gasLimit uint64) error {

    // 멤풀에 추가
    if err := r.mempool.AddTxWithMeta(txBytes, sender, nonce,
        gasPrice, gasLimit); err != nil {
        return err
    }

    // 브로드캐스트 큐에 추가
    if r.config.BroadcastEnabled {
        tx := NewTxWithMeta(txBytes, sender, nonce, gasPrice, gasLimit)
        select {
        case r.broadcastQueue <- tx:
        default:
            // 큐 가득 참 - 무시 (멤풀에는 이미 추가됨)
        }
    }
    return nil
}

// 4. mempool/mempool.go - AddTxWithMeta()
func (m *Mempool) AddTxWithMeta(txBytes []byte, sender string,
    nonce, gasPrice, gasLimit uint64) error {

    tx := NewTxWithMeta(txBytes, sender, nonce, gasPrice, gasLimit)

    m.mu.Lock()
    defer m.mu.Unlock()

    // 검증
    if err := m.validateTxLocked(tx); err != nil {
        return err
    }

    // CheckTx 콜백 (ABCI)
    if m.checkTxCallback != nil {
        if err := m.checkTxCallback(tx.Data); err != nil {
            return fmt.Errorf("CheckTx failed: %w", err)
        }
    }

    // 멤풀에 추가
    return m.addTxLocked(tx)
}
```

### 2.2 피어로부터 수신 경로

```
┌─────────────────────────────────────────────────────────────────┐
│              RECEIVE FROM PEER                                   │
│                                                                  │
│  Peer          Transport       Reactor        Mempool           │
│    │               │              │              │               │
│    │ TxMessage     │              │              │               │
│    │──────────────►│              │              │               │
│    │               │              │              │               │
│    │               │ ReceiveTx    │              │               │
│    │               │─────────────►│              │               │
│    │               │              │              │               │
│    │               │              │ AddTx        │               │
│    │               │              │─────────────►│               │
│    │               │              │              │               │
│    │               │              │              │ (CheckTx)     │
│    │               │              │              │───────┐       │
│    │               │              │              │       │       │
│    │               │              │              │◄──────┘       │
│    │               │              │              │               │
│    │               │              │◄─────────────│               │
│    │               │              │              │               │
│    │               │◄─────────────│              │               │
│    │               │  (no broadcast - already in network)       │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

**코드 경로:**
```go
// mempool/reactor.go:235 - ReceiveTx()
func (r *Reactor) ReceiveTx(peerID string, txBytes []byte) error {
    // 멤풀에 추가 (브로드캐스트 하지 않음)
    err := r.mempool.AddTx(txBytes)
    if err != nil {
        // 이미 존재하면 정상 (다른 피어에서도 받았을 수 있음)
        if err == ErrTxAlreadyExists {
            return nil
        }
        return err
    }
    return nil
}
```

## 3. 멤풀 내부 처리

### 3.1 트랜잭션 검증

```go
// mempool/mempool.go - validateTxLocked()
func (m *Mempool) validateTxLocked(tx *Tx) error {
    // 1. 크기 검증
    if int64(len(tx.Data)) > m.config.MaxTxBytes {
        return ErrTxTooLarge
    }

    // 2. 중복 검사
    if _, exists := m.txStore[tx.ID]; exists {
        return ErrTxAlreadyExists
    }

    // 3. 멤풀 용량 검사
    if len(m.txStore) >= m.config.MaxTxs {
        return ErrMempoolFull
    }

    // 4. 총 바이트 검사
    if m.currentBytes + int64(len(tx.Data)) > m.config.MaxBytes {
        return ErrMempoolFull
    }

    return nil
}
```

### 3.2 트랜잭션 저장

```go
// mempool/mempool.go - addTxLocked()
func (m *Mempool) addTxLocked(tx *Tx) error {
    // 1. txStore에 저장
    m.txStore[tx.ID] = tx

    // 2. senderIndex에 추가
    m.senderIndex[tx.Sender] = append(m.senderIndex[tx.Sender], tx)

    // 3. 바이트 수 및 카운트 업데이트
    m.txCount++
    m.txBytes += int64(len(tx.Data))

    // 4. 발신자 nonce 업데이트
    if tx.Nonce > m.senderNonce[tx.Sender] {
        m.senderNonce[tx.Sender] = tx.Nonce
    }

    // 5. 새 트랜잭션 채널 알림
    select {
    case m.newTxCh <- tx:
    default:
    }

    return nil
}
```

### 3.3 ABCI 2.0 설계 철학 - FIFO 저장

**핵심 개념:**
- **Mempool**: FIFO 순서로 트랜잭션 저장 (단순성, O(1) 삽입)
- **PrepareProposal**: ABCI 앱에서 트랜잭션 정렬/필터링 담당 (유연성)

```
┌─────────────────────────────────────────────────────────────────┐
│              ABCI 2.0 TRANSACTION ORDERING                      │
│                                                                  │
│  Mempool (FIFO)          ABCI App (PrepareProposal)            │
│  ┌─────────────┐         ┌─────────────────────────┐           │
│  │ Tx1 (t=0)   │         │ 1. 가스 가격으로 정렬    │           │
│  │ Tx2 (t=1)   │ ─────►  │ 2. 발신자별 nonce 검증   │           │
│  │ Tx3 (t=2)   │         │ 3. 의존성 순서 처리      │           │
│  │ ...         │         │ 4. 최종 트랜잭션 목록    │           │
│  └─────────────┘         └─────────────────────────┘           │
│                                                                  │
│  단순 저장               비즈니스 로직 적용                     │
└─────────────────────────────────────────────────────────────────┘
```

이 접근법의 장점:
- 합의 엔진과 애플리케이션 로직의 분리
- 앱별 맞춤형 트랜잭션 정렬 가능
- CometBFT 방식과 일관성 유지

## 4. 블록 제안 시 트랜잭션 수집

```
┌─────────────────────────────────────────────────────────────────┐
│              REAP TRANSACTIONS FOR PROPOSAL                      │
│                                                                  │
│  Engine          Mempool         ABCIAdapter                    │
│     │               │                 │                          │
│     │ ReapMaxTxs    │                 │                          │
│     │──────────────►│                 │                          │
│     │               │                 │                          │
│     │               │ (pop from queue)│                          │
│     │               │───────┐         │                          │
│     │               │       │         │                          │
│     │               │◄──────┘         │                          │
│     │               │                 │                          │
│     │◄──────────────│                 │                          │
│     │   [][]byte    │                 │                          │
│     │               │                 │                          │
│     │ PrepareProposal                 │                          │
│     │────────────────────────────────►│                          │
│     │               │                 │                          │
│     │◄────────────────────────────────│                          │
│     │   ordered txs │                 │                          │
│     │               │                 │                          │
└─────────────────────────────────────────────────────────────────┘
```

**코드 경로:**
```go
// mempool/mempool.go - ReapMaxTxs()
func (m *Mempool) ReapMaxTxs(maxTxs int) [][]byte {
    m.mu.RLock()
    defer m.mu.RUnlock()

    if maxTxs <= 0 {
        maxTxs = m.config.MaxTxs
    }

    txs := make([][]byte, 0, maxTxs)

    // 우선순위 큐에서 순서대로 추출
    for i := 0; i < maxTxs && m.txQueue.Len() > 0; i++ {
        tx := heap.Pop(m.txQueue).(*Tx)
        txs = append(txs, tx.Data)
    }

    return txs
}

// consensus/pbft/abci_adapter.go:92 - PrepareProposal()
func (a *ABCIAdapter) PrepareProposal(ctx context.Context,
    height int64, proposer []byte, txs [][]byte) ([][]byte, error) {

    req := abciclient.NewPrepareProposalRequest(
        txs,
        a.maxTxBytes,
        height,
        time.Now(),
        proposer,
    )

    resp, err := a.client.PrepareProposal(ctx, req)
    if err != nil {
        return nil, fmt.Errorf("PrepareProposal failed: %w", err)
    }

    return resp.Txs, nil
}
```

## 5. 트랜잭션 실행

### 5.1 FinalizeBlock 플로우

```
┌─────────────────────────────────────────────────────────────────┐
│                  FINALIZE BLOCK                                  │
│                                                                  │
│  Engine         ABCIAdapter        ABCIClient        App        │
│     │                │                 │              │          │
│     │ FinalizeBlock  │                 │              │          │
│     │───────────────►│                 │              │          │
│     │                │                 │              │          │
│     │                │ FinalizeBlock   │              │          │
│     │                │────────────────►│              │          │
│     │                │                 │              │          │
│     │                │                 │ FinalizeBlock│          │
│     │                │                 │─────────────►│          │
│     │                │                 │              │          │
│     │                │                 │              │ Execute  │
│     │                │                 │              │ each tx  │
│     │                │                 │              │───┐      │
│     │                │                 │              │   │      │
│     │                │                 │              │◄──┘      │
│     │                │                 │              │          │
│     │                │                 │◄─────────────│          │
│     │                │                 │  TxResults   │          │
│     │                │                 │  AppHash     │          │
│     │                │                 │  Events      │          │
│     │                │                 │              │          │
│     │                │◄────────────────│              │          │
│     │                │                 │              │          │
│     │◄───────────────│                 │              │          │
│     │  ExecutionResult                 │              │          │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

**코드 경로:**
```go
// consensus/pbft/abci_adapter.go:130 - FinalizeBlock()
func (a *ABCIAdapter) FinalizeBlock(ctx context.Context,
    block *types.Block) (*ABCIExecutionResult, error) {

    // 트랜잭션 추출
    txs := make([][]byte, len(block.Transactions))
    for i, tx := range block.Transactions {
        txs[i] = tx.Data
    }

    blockData := &abciclient.BlockData{
        Height:       int64(block.Header.Height),
        Txs:          txs,
        Hash:         block.Hash,
        Time:         block.Header.Timestamp,
        ProposerAddr: []byte(block.Header.ProposerID),
    }

    req := abciclient.NewFinalizeBlockRequest(blockData)

    resp, err := a.client.FinalizeBlock(ctx, req)
    if err != nil {
        return nil, fmt.Errorf("FinalizeBlock failed: %w", err)
    }

    result := abciclient.FinalizeBlockResponseToResult(resp)

    return &ABCIExecutionResult{
        TxResults:        convertTxResults(result.TxResults),
        ValidatorUpdates: result.ValidatorUpdates,
        AppHash:          result.AppHash,
        Events:           result.Events,
    }, nil
}
```

### 5.2 Commit 플로우

```go
// consensus/pbft/abci_adapter.go:164 - Commit()
func (a *ABCIAdapter) Commit(ctx context.Context) (
    appHash []byte, retainHeight int64, err error) {

    resp, err := a.client.Commit(ctx)
    if err != nil {
        return nil, 0, fmt.Errorf("Commit failed: %w", err)
    }

    a.mu.Lock()
    a.lastHeight++
    a.mu.Unlock()

    return a.lastAppHash, resp.RetainHeight, nil
}
```

## 6. 트랜잭션 제거

```
┌─────────────────────────────────────────────────────────────────┐
│              REMOVE COMMITTED TRANSACTIONS                       │
│                                                                  │
│  Engine                          Mempool                         │
│     │                               │                            │
│     │ RemoveTxs(committedTxs)       │                            │
│     │──────────────────────────────►│                            │
│     │                               │                            │
│     │                               │ for each tx:               │
│     │                               │   delete from txStore      │
│     │                               │   delete from senderIndex  │
│     │                               │   update metrics           │
│     │                               │───────┐                    │
│     │                               │       │                    │
│     │                               │◄──────┘                    │
│     │                               │                            │
│     │◄──────────────────────────────│                            │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

**코드 경로:**
```go
// mempool/mempool.go - RemoveTxs()
func (m *Mempool) RemoveTxs(txIDs []string) {
    m.mu.Lock()
    defer m.mu.Unlock()

    for _, id := range txIDs {
        if tx, exists := m.txStore[id]; exists {
            // txStore에서 제거
            delete(m.txStore, id)

            // senderIndex에서 제거
            m.removeTxFromSenderIndex(tx)

            // 바이트 수 업데이트
            m.currentBytes -= int64(len(tx.Data))

            // 메트릭 업데이트
            if m.metrics != nil {
                m.metrics.TxCount.Dec()
                m.metrics.MempoolSize.Set(float64(len(m.txStore)))
            }
        }
    }
}

// mempool/mempool.go - RemoveByHashes()
func (m *Mempool) RemoveByHashes(hashes [][]byte) {
    txIDs := make([]string, len(hashes))
    for i, hash := range hashes {
        txIDs[i] = hex.EncodeToString(hash)
    }
    m.RemoveTxs(txIDs)
}
```

## 7. 트랜잭션 브로드캐스트

```
┌─────────────────────────────────────────────────────────────────┐
│              TRANSACTION BROADCAST                               │
│                                                                  │
│  Reactor              Broadcaster          Peers                │
│     │                      │                 │                   │
│     │ broadcastLoop()      │                 │                   │
│     │───────┐              │                 │                   │
│     │       │              │                 │                   │
│     │       │ collect batch│                 │                   │
│     │       │              │                 │                   │
│     │◄──────┘              │                 │                   │
│     │                      │                 │                   │
│     │ broadcastBatch()     │                 │                   │
│     │─────────────────────►│                 │                   │
│     │                      │                 │                   │
│     │                      │ for each tx:    │                   │
│     │                      │ BroadcastTx     │                   │
│     │                      │────────────────►│                   │
│     │                      │                 │                   │
│     │◄─────────────────────│                 │                   │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

**코드 경로:**
```go
// mempool/reactor.go:249 - broadcastLoop()
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

// mempool/reactor.go:279 - broadcastBatch()
func (r *Reactor) broadcastBatch(batch []*Tx) {
    r.mu.RLock()
    broadcaster := r.broadcaster
    r.mu.RUnlock()

    if broadcaster == nil {
        return
    }

    for _, tx := range batch {
        if err := broadcaster.BroadcastTx(tx.Data); err != nil {
            fmt.Printf("[Reactor] Failed to broadcast tx %s: %v\n",
                tx.ID[:8], err)
        }
    }
}
```

## 8. 에러 처리 케이스

### 8.1 트랜잭션 제출 실패 케이스

| 에러 | 원인 | 처리 |
|------|------|------|
| `ErrTxAlreadyExists` | 중복 트랜잭션 | 무시 (정상) |
| `ErrMempoolFull` | 멤풀 용량 초과 | 거부, 클라이언트 재시도 |
| `ErrTxTooLarge` | 트랜잭션 크기 초과 | 거부 |
| `CheckTx failed` | ABCI 검증 실패 | 거부, 로그 기록 |

### 8.2 브로드캐스트 실패 케이스

| 상황 | 처리 |
|------|------|
| 큐 가득 참 | 무시 (멤풀에는 저장됨) |
| 피어 연결 실패 | 로그 기록, 계속 진행 |
| 네트워크 오류 | 로그 기록, 다음 배치에서 재시도 |
