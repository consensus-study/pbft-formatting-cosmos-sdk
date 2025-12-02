# ABCI 2.0 코드 플로우 분석

## 1. ABCI 2.0 아키텍처 개요

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                       ABCI 2.0 ARCHITECTURE                                  │
│                                                                              │
│   ┌─────────────────────────────────────────────────────────────────────┐   │
│   │                    PBFT Consensus Engine                             │   │
│   │                                                                      │   │
│   │  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐               │   │
│   │  │   Leader     │  │   Replica    │  │   Replica    │               │   │
│   │  │  (Proposer)  │  │  (Validator) │  │  (Validator) │               │   │
│   │  └──────┬───────┘  └──────┬───────┘  └──────┬───────┘               │   │
│   │         │                 │                 │                        │   │
│   └─────────┼─────────────────┼─────────────────┼────────────────────────┘   │
│             │                 │                 │                             │
│             │                 │                 │                             │
│   ┌─────────▼─────────────────▼─────────────────▼────────────────────────┐   │
│   │                      ABCI Adapter                                     │   │
│   │                                                                       │   │
│   │   PrepareProposal  ProcessProposal  FinalizeBlock  Commit  CheckTx   │   │
│   │                                                                       │   │
│   └───────────────────────────────┬───────────────────────────────────────┘   │
│                                   │                                           │
│                                   │ gRPC                                      │
│                                   │                                           │
│   ┌───────────────────────────────▼───────────────────────────────────────┐   │
│   │                        ABCI Application                                │   │
│   │                      (Cosmos SDK App)                                  │   │
│   │                                                                        │   │
│   │   State Machine  │  Transaction Processing  │  State Storage          │   │
│   │                                                                        │   │
│   └────────────────────────────────────────────────────────────────────────┘   │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

## 2. ABCI 2.0 메서드 호출 순서

```
┌─────────────────────────────────────────────────────────────────┐
│              ABCI 2.0 METHOD CALL SEQUENCE                       │
│                                                                  │
│   초기화:                                                         │
│   ┌──────────────┐                                               │
│   │  InitChain   │ ─── 체인 시작 시 1회 호출                       │
│   └──────────────┘                                               │
│                                                                  │
│   블록 생성 루프:                                                  │
│                                                                  │
│   ┌──────────────┐                                               │
│   │   CheckTx    │ ─── 트랜잭션이 멤풀에 들어올 때                  │
│   └──────────────┘     (멤풀 검증용)                              │
│          │                                                       │
│          ▼                                                       │
│   ┌──────────────────┐                                           │
│   │ PrepareProposal  │ ─── 리더가 블록 제안 시 (ABCI 2.0 신규)     │
│   └──────────────────┘     (트랜잭션 정렬/필터링)                  │
│          │                                                       │
│          ▼                                                       │
│   ┌──────────────────┐                                           │
│   │ ProcessProposal  │ ─── 제안된 블록 검증 시 (ABCI 2.0 신규)     │
│   └──────────────────┘     (ACCEPT/REJECT 결정)                  │
│          │                                                       │
│          ▼                                                       │
│   ┌──────────────────┐                                           │
│   │  FinalizeBlock   │ ─── 블록 확정 시 (ABCI 2.0 통합)           │
│   └──────────────────┘     (BeginBlock+DeliverTx+EndBlock 통합)  │
│          │                                                       │
│          ▼                                                       │
│   ┌──────────────┐                                               │
│   │    Commit    │ ─── 상태 영구 저장                             │
│   └──────────────┘                                               │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

## 3. InitChain 플로우

```
┌─────────────────────────────────────────────────────────────────┐
│                      INIT CHAIN                                  │
│                                                                  │
│  Node         ABCIAdapter        Client          App            │
│    │               │                │              │             │
│    │ InitChain     │                │              │             │
│    │──────────────►│                │              │             │
│    │               │                │              │             │
│    │               │ InitChain      │              │             │
│    │               │───────────────►│              │             │
│    │               │                │              │             │
│    │               │                │ InitChain    │             │
│    │               │                │─────────────►│             │
│    │               │                │              │             │
│    │               │                │              │ Initialize  │
│    │               │                │              │───────┐     │
│    │               │                │              │       │     │
│    │               │                │              │◄──────┘     │
│    │               │                │              │             │
│    │               │                │◄─────────────│             │
│    │               │                │  AppHash     │             │
│    │               │                │  Validators  │             │
│    │               │                │              │             │
│    │               │◄───────────────│              │             │
│    │               │                │              │             │
│    │◄──────────────│                │              │             │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

**코드 경로:**
```go
// consensus/pbft/abci_adapter.go:54 - InitChain()
func (a *ABCIAdapter) InitChain(ctx context.Context, chainID string,
    validators []*types.Validator, appState []byte) error {

    a.chainID = chainID

    // Validator를 ABCI 타입으로 변환
    abciValidators := make([]abci.ValidatorUpdate, len(validators))
    for i, v := range validators {
        abciValidators[i] = abci.ValidatorUpdate{
            PubKey: abci.PubKey{
                Type: "ed25519",
                Data: v.PublicKey,
            },
            Power: v.Power,
        }
    }

    req := &abci.RequestInitChain{
        ChainId:       chainID,
        Validators:    abciValidators,
        Time:          time.Now(),
        AppStateBytes: appState,
        InitialHeight: 1,
    }

    resp, err := a.client.InitChain(ctx, req)
    if err != nil {
        return fmt.Errorf("InitChain failed: %w", err)
    }

    a.mu.Lock()
    a.lastAppHash = resp.AppHash
    a.lastHeight = 0
    a.mu.Unlock()

    return nil
}
```

## 4. CheckTx 플로우

```
┌─────────────────────────────────────────────────────────────────┐
│                       CHECK TX                                   │
│                                                                  │
│  Mempool       ABCIAdapter        Client          App           │
│     │               │                │              │            │
│     │ CheckTx(tx)   │                │              │            │
│     │──────────────►│                │              │            │
│     │               │                │              │            │
│     │               │ CheckTx        │              │            │
│     │               │───────────────►│              │            │
│     │               │                │              │            │
│     │               │                │ CheckTx      │            │
│     │               │                │─────────────►│            │
│     │               │                │              │            │
│     │               │                │              │ Validate   │
│     │               │                │              │───────┐    │
│     │               │                │              │       │    │
│     │               │                │              │◄──────┘    │
│     │               │                │              │            │
│     │               │                │◄─────────────│            │
│     │               │                │  Code        │            │
│     │               │                │  GasWanted   │            │
│     │               │                │              │            │
│     │               │◄───────────────│              │            │
│     │               │                │              │            │
│     │◄──────────────│                │              │            │
│     │  error/nil    │                │              │            │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

**코드 경로:**
```go
// consensus/pbft/abci_adapter.go:178 - CheckTx()
func (a *ABCIAdapter) CheckTx(ctx context.Context, tx []byte) error {
    resp, err := a.client.CheckTx(ctx, tx)
    if err != nil {
        return err
    }

    if resp.Code != 0 {
        return fmt.Errorf("CheckTx failed (code=%d): %s",
            resp.Code, resp.Log)
    }

    return nil
}

// abci/client.go - CheckTx()
func (c *Client) CheckTx(ctx context.Context, tx []byte) (
    *abci.ResponseCheckTx, error) {

    ctx, cancel := context.WithTimeout(ctx, c.config.Timeout)
    defer cancel()

    req := &abci.RequestCheckTx{
        Tx:   tx,
        Type: abci.CheckTxType_New,
    }

    return c.abciClient.CheckTx(ctx, req)
}
```

**CheckTx 응답 코드:**
| Code | 의미 | 처리 |
|------|------|------|
| 0 | 성공 | 멤풀에 추가 |
| 1+ | 실패 | 멤풀 거부, 에러 로그 |

## 5. PrepareProposal 플로우 (ABCI 2.0)

```
┌─────────────────────────────────────────────────────────────────┐
│                  PREPARE PROPOSAL                                │
│                                                                  │
│  Engine        ABCIAdapter        Client          App           │
│    │               │                │              │             │
│    │ PrepareProposal               │              │             │
│    │ (txs, height) │                │              │             │
│    │──────────────►│                │              │             │
│    │               │                │              │             │
│    │               │ PrepareProposal│              │             │
│    │               │───────────────►│              │             │
│    │               │                │              │             │
│    │               │                │ PrepareProposal            │
│    │               │                │─────────────►│             │
│    │               │                │              │             │
│    │               │                │              │ Reorder     │
│    │               │                │              │ Filter      │
│    │               │                │              │ Add new     │
│    │               │                │              │───────┐     │
│    │               │                │              │       │     │
│    │               │                │              │◄──────┘     │
│    │               │                │              │             │
│    │               │                │◄─────────────│             │
│    │               │                │  ordered txs │             │
│    │               │                │              │             │
│    │               │◄───────────────│              │             │
│    │               │                │              │             │
│    │◄──────────────│                │              │             │
│    │  ordered txs  │                │              │             │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

**코드 경로:**
```go
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

// abci/types.go - NewPrepareProposalRequest()
func NewPrepareProposalRequest(txs [][]byte, maxTxBytes int64,
    height int64, time time.Time, proposer []byte) *abci.RequestPrepareProposal {

    return &abci.RequestPrepareProposal{
        Txs:             txs,
        MaxTxBytes:      maxTxBytes,
        Height:          height,
        Time:            time,
        ProposerAddress: proposer,
    }
}
```

**PrepareProposal 앱 처리:**
- 트랜잭션 재정렬 (수수료, 우선순위)
- 유효하지 않은 트랜잭션 필터링
- 앱 자체 트랜잭션 추가 가능 (예: 오라클 데이터)
- maxTxBytes 초과 시 트랜잭션 제거

## 6. ProcessProposal 플로우 (ABCI 2.0)

```
┌─────────────────────────────────────────────────────────────────┐
│                  PROCESS PROPOSAL                                │
│                                                                  │
│  Engine        ABCIAdapter        Client          App           │
│    │               │                │              │             │
│    │ ProcessProposal               │              │             │
│    │ (txs, height) │                │              │             │
│    │──────────────►│                │              │             │
│    │               │                │              │             │
│    │               │ ProcessProposal│              │             │
│    │               │───────────────►│              │             │
│    │               │                │              │             │
│    │               │                │ ProcessProposal            │
│    │               │                │─────────────►│             │
│    │               │                │              │             │
│    │               │                │              │ Validate    │
│    │               │                │              │ Block       │
│    │               │                │              │───────┐     │
│    │               │                │              │       │     │
│    │               │                │              │◄──────┘     │
│    │               │                │              │             │
│    │               │                │◄─────────────│             │
│    │               │                │ ACCEPT/REJECT│             │
│    │               │                │              │             │
│    │               │◄───────────────│              │             │
│    │               │                │              │             │
│    │◄──────────────│                │              │             │
│    │  true/false   │                │              │             │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

**코드 경로:**
```go
// consensus/pbft/abci_adapter.go:111 - ProcessProposal()
func (a *ABCIAdapter) ProcessProposal(ctx context.Context,
    height int64, proposer []byte, txs [][]byte, hash []byte) (bool, error) {

    req := abciclient.NewProcessProposalRequest(
        txs,
        height,
        time.Now(),
        proposer,
        hash,
    )

    resp, err := a.client.ProcessProposal(ctx, req)
    if err != nil {
        return false, fmt.Errorf("ProcessProposal failed: %w", err)
    }

    return resp.Status == abci.ResponseProcessProposal_ACCEPT, nil
}
```

**ProcessProposal 결과:**
| Status | 의미 | PBFT 동작 |
|--------|------|----------|
| ACCEPT | 제안 수락 | Prepare 메시지 브로드캐스트 |
| REJECT | 제안 거부 | 제안 무시, 뷰 변경 가능 |

## 7. FinalizeBlock 플로우 (ABCI 2.0)

```
┌─────────────────────────────────────────────────────────────────┐
│                  FINALIZE BLOCK                                  │
│                                                                  │
│  Engine        ABCIAdapter        Client          App           │
│    │               │                │              │             │
│    │ FinalizeBlock │                │              │             │
│    │ (block)       │                │              │             │
│    │──────────────►│                │              │             │
│    │               │                │              │             │
│    │               │ FinalizeBlock  │              │             │
│    │               │───────────────►│              │             │
│    │               │                │              │             │
│    │               │                │ FinalizeBlock│             │
│    │               │                │─────────────►│             │
│    │               │                │              │             │
│    │               │                │              │ BeginBlock  │
│    │               │                │              │───────┐     │
│    │               │                │              │       │     │
│    │               │                │              │◄──────┘     │
│    │               │                │              │             │
│    │               │                │              │ DeliverTx   │
│    │               │                │              │ (for each)  │
│    │               │                │              │───────┐     │
│    │               │                │              │       │     │
│    │               │                │              │◄──────┘     │
│    │               │                │              │             │
│    │               │                │              │ EndBlock    │
│    │               │                │              │───────┐     │
│    │               │                │              │       │     │
│    │               │                │              │◄──────┘     │
│    │               │                │              │             │
│    │               │                │◄─────────────│             │
│    │               │                │  TxResults   │             │
│    │               │                │  AppHash     │             │
│    │               │                │  ValUpdates  │             │
│    │               │                │  Events      │             │
│    │               │                │              │             │
│    │               │◄───────────────│              │             │
│    │               │                │              │             │
│    │◄──────────────│                │              │             │
│    │ ExecutionResult               │              │             │
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

// abci/types.go - NewFinalizeBlockRequest()
func NewFinalizeBlockRequest(block *BlockData) *abci.RequestFinalizeBlock {
    return &abci.RequestFinalizeBlock{
        Txs:             block.Txs,
        Height:          block.Height,
        Time:            block.Time,
        Hash:            block.Hash,
        ProposerAddress: block.ProposerAddr,
    }
}
```

## 8. Commit 플로우

```
┌─────────────────────────────────────────────────────────────────┐
│                       COMMIT                                     │
│                                                                  │
│  Engine        ABCIAdapter        Client          App           │
│    │               │                │              │             │
│    │ Commit        │                │              │             │
│    │──────────────►│                │              │             │
│    │               │                │              │             │
│    │               │ Commit         │              │             │
│    │               │───────────────►│              │             │
│    │               │                │              │             │
│    │               │                │ Commit       │             │
│    │               │                │─────────────►│             │
│    │               │                │              │             │
│    │               │                │              │ Persist     │
│    │               │                │              │ State       │
│    │               │                │              │───────┐     │
│    │               │                │              │       │     │
│    │               │                │              │◄──────┘     │
│    │               │                │              │             │
│    │               │                │◄─────────────│             │
│    │               │                │ RetainHeight │             │
│    │               │                │              │             │
│    │               │◄───────────────│              │             │
│    │               │                │              │             │
│    │◄──────────────│                │              │             │
│    │ appHash       │                │              │             │
│    │ retainHeight  │                │              │             │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

**코드 경로:**
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

// abci/client.go - Commit()
func (c *Client) Commit(ctx context.Context) (*abci.ResponseCommit, error) {
    ctx, cancel := context.WithTimeout(ctx, c.config.Timeout)
    defer cancel()

    return c.abciClient.Commit(ctx, &abci.RequestCommit{})
}
```

## 9. Query 플로우

```
┌─────────────────────────────────────────────────────────────────┐
│                       QUERY                                      │
│                                                                  │
│  Client        ABCIAdapter        Client          App           │
│    │               │                │              │             │
│    │ Query(path)   │                │              │             │
│    │──────────────►│                │              │             │
│    │               │                │              │             │
│    │               │ Query          │              │             │
│    │               │───────────────►│              │             │
│    │               │                │              │             │
│    │               │                │ Query        │             │
│    │               │                │─────────────►│             │
│    │               │                │              │             │
│    │               │                │              │ Read State  │
│    │               │                │              │───────┐     │
│    │               │                │              │       │     │
│    │               │                │              │◄──────┘     │
│    │               │                │              │             │
│    │               │                │◄─────────────│             │
│    │               │                │  Key/Value   │             │
│    │               │                │  Proof       │             │
│    │               │                │              │             │
│    │               │◄───────────────│              │             │
│    │               │                │              │             │
│    │◄──────────────│                │              │             │
│    │  QueryResult  │                │              │             │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

**코드 경로:**
```go
// consensus/pbft/abci_adapter.go:192 - Query()
func (a *ABCIAdapter) Query(ctx context.Context, path string,
    data []byte, height int64, prove bool) (*ABCIQueryResult, error) {

    resp, err := a.client.Query(ctx, &abci.RequestQuery{
        Path:   path,
        Data:   data,
        Height: height,
        Prove:  prove,
    })
    if err != nil {
        return nil, err
    }

    if resp.Code != 0 {
        return nil, fmt.Errorf("query failed (code=%d): %s",
            resp.Code, resp.Log)
    }

    return &ABCIQueryResult{
        Key:    resp.Key,
        Value:  resp.Value,
        Height: resp.Height,
    }, nil
}
```

## 10. ABCI 1.0 vs ABCI 2.0 비교

| 기능 | ABCI 1.0 | ABCI 2.0 |
|------|----------|----------|
| 블록 제안 | 합의 엔진이 결정 | `PrepareProposal`으로 앱이 참여 |
| 블록 검증 | 합의 엔진만 검증 | `ProcessProposal`으로 앱이 참여 |
| 블록 실행 | BeginBlock → DeliverTx → EndBlock | `FinalizeBlock` 단일 호출 |
| 상태 커밋 | Commit | Commit (동일) |
| 트랜잭션 검증 | CheckTx | CheckTx (동일) |

**ABCI 2.0 장점:**
1. 앱이 블록 내용에 더 많은 제어권
2. 단일 FinalizeBlock 호출로 성능 향상
3. MEV(Miner Extractable Value) 방지 가능
4. 앱 자체 트랜잭션 삽입 가능
