# PBFT 합의 코드 플로우 (Mermaid 다이어그램)

## 1. PBFT 합의 전체 흐름

```mermaid
sequenceDiagram
    autonumber
    participant C as Client
    participant L as Leader (Primary)
    participant R1 as Replica 1
    participant R2 as Replica 2
    participant R3 as Replica 3

    Note over C,R3: Phase 1: Request
    C->>L: SubmitTx(tx, clientID)

    Note over C,R3: Phase 2: Pre-Prepare
    L->>L: proposeBlock()
    L->>R1: PRE-PREPARE (view, seq, digest, block)
    L->>R2: PRE-PREPARE
    L->>R3: PRE-PREPARE

    Note over C,R3: Phase 3: Prepare
    R1->>L: PREPARE (view, seq, digest)
    R1->>R2: PREPARE
    R1->>R3: PREPARE
    R2->>L: PREPARE
    R2->>R1: PREPARE
    R2->>R3: PREPARE
    R3->>L: PREPARE
    R3->>R1: PREPARE
    R3->>R2: PREPARE

    Note over C,R3: 2f+1 Prepares 수집 → PREPARED 상태

    Note over C,R3: Phase 4: Commit
    L->>R1: COMMIT (view, seq, digest)
    L->>R2: COMMIT
    L->>R3: COMMIT
    R1->>L: COMMIT
    R1->>R2: COMMIT
    R1->>R3: COMMIT
    R2->>L: COMMIT
    R2->>R1: COMMIT
    R2->>R3: COMMIT
    R3->>L: COMMIT
    R3->>R1: COMMIT
    R3->>R2: COMMIT

    Note over C,R3: 2f+1 Commits 수집 → COMMITTED 상태

    Note over C,R3: Phase 5: Execute & Reply
    L->>L: executeBlock()
    R1->>R1: executeBlock()
    R2->>R2: executeBlock()
    R3->>R3: executeBlock()
    L-->>C: Reply (result)
```

---

## 2. 단계별 상세 코드 플로우

### 2.1 Phase 1: Request (트랜잭션 제출)

```mermaid
flowchart TB
    subgraph Client["클라이언트"]
        A[트랜잭션 생성]
    end

    subgraph Node["Node (node/node.go)"]
        B[SubmitTx<br/>line:340]
        C{mempool != nil?}
        D[engine.SubmitRequest<br/>직접 전달]
    end

    subgraph Mempool["Mempool (mempool/mempool.go)"]
        E[AddTxWithMeta<br/>line:269]
        F[NewTxWithMeta<br/>Tx 객체 생성]
        G[validateTx<br/>크기/중복 검증]
        H{검증 통과?}
        I[addTxLocked<br/>txStore에 저장]
        J[newTxCh <- tx<br/>Reactor 알림]
        K[에러 반환]
    end

    subgraph Reactor["Reactor (mempool/reactor.go)"]
        L[broadcastLoop]
        M[BroadcastTx<br/>P2P 전파]
    end

    subgraph Engine["Engine (consensus/pbft/engine.go)"]
        N{IsPrimary?}
        O[SubmitRequest<br/>requestChan에 추가]
        P[proposeBlock 트리거]
        Q[대기<br/>리더가 수집할 때까지]
    end

    A --> B
    B --> C
    C -->|No| D
    C -->|Yes| E
    E --> F
    F --> G
    G --> H
    H -->|No| K
    H -->|Yes| I
    I --> J
    J --> L
    L --> M

    I --> N
    N -->|Yes| O
    O --> P
    N -->|No| Q
    D --> O

    style A fill:#e1f5fe
    style P fill:#c8e6c9
    style K fill:#ffcdd2
```

**코드 경로 상세:**

```mermaid
flowchart LR
    subgraph "node/node.go:340-357"
        A["SubmitTx(tx []byte, clientID string)"]
    end

    subgraph "mempool/mempool.go:269-344"
        B["AddTxWithMeta()"]
        B1["1. 크기 체크 (MaxTxBytes)"]
        B2["2. Tx 생성 (NewTxWithMeta)"]
        B3["3. 중복 체크 (txStore)"]
        B4["4. 최근 제거 체크"]
        B5["5. 가스 가격 체크"]
        B6["6. Nonce 체크"]
        B7["7. CheckTx 콜백"]
        B8["8. 용량 체크/퇴출"]
        B9["9. 저장 (addTxLocked)"]
        B10["10. 알림 (newTxCh)"]
    end

    A --> B
    B --> B1 --> B2 --> B3 --> B4 --> B5 --> B6 --> B7 --> B8 --> B9 --> B10
```

---

### 2.2 Phase 2: Pre-Prepare (블록 제안)

```mermaid
flowchart TB
    subgraph Engine["Engine - 리더 (consensus/pbft/engine.go)"]
        A[run 루프<br/>line:229]
        B[requestChan에서<br/>요청 수신]
        C{IsPrimary?}
        D[proposeBlock<br/>line:310]
    end

    subgraph ProposeBlock["proposeBlock 상세"]
        E[sequenceNum++<br/>블록 높이 증가]
        F{mempool != nil?}
        G[ReapMaxTxs 500<br/>FIFO로 수집]
        H[app.GetPendingTxs<br/>하위 호환성]
        I[요청 tx 추가]
        J[CreateBlock<br/>블록 생성]
        K[블록 해시 계산]
    end

    subgraph Broadcast["브로드캐스트"]
        L[PrePrepareMsg 생성]
        M[transport.Broadcast]
        N[모든 Replica에게 전송]
    end

    subgraph StateLog["StateLog 저장"]
        O[GetOrCreateState]
        P[SetPrePrepare]
        Q[Phase → PrePrepared]
    end

    A --> B
    B --> C
    C -->|No| A
    C -->|Yes| D
    D --> E
    E --> F
    F -->|Yes| G
    F -->|No| H
    G --> I
    H --> I
    I --> J
    J --> K
    K --> L
    L --> M
    M --> N
    K --> O
    O --> P
    P --> Q

    style D fill:#fff9c4
    style G fill:#c8e6c9
    style N fill:#bbdefb
```

**Mempool에서 트랜잭션 수집:**

```mermaid
sequenceDiagram
    participant E as Engine
    participant M as Mempool
    participant T as types.Transaction

    E->>M: ReapMaxTxs(500)
    M->>M: txStore 순회
    M->>M: Timestamp 기준 FIFO 정렬
    M->>M: 최대 500개 선택
    M-->>E: []*Tx 반환

    loop 각 mempool Tx
        E->>T: 변환
        Note over E,T: mptx.ID → tx.ID<br/>mptx.Data → tx.Data<br/>mptx.Timestamp → tx.Timestamp<br/>mptx.Sender → tx.From
    end

    E->>E: CreateBlock(txs)
```

---

### 2.3 Phase 3: Prepare (투표)

```mermaid
flowchart TB
    subgraph Receive["메시지 수신"]
        A[handleIncomingMessage<br/>line:251]
        B[msgChan <- msg]
        C[handleMessage<br/>line:262]
        D{msg.Type?}
    end

    subgraph HandlePrePrepare["handlePrePrepare (line:392)"]
        E[뷰 번호 확인]
        F[메시지 디코딩<br/>JSON → PrePrepareMsg]
        G[리더 검증<br/>PrimaryID == getPrimaryID]
        H[다이제스트 검증<br/>block.Hash == msg.Digest]
        I[stateLog.GetOrCreateState]
        J[state.SetPrePrepare]
    end

    subgraph BroadcastPrepare["Prepare 브로드캐스트"]
        K[PrepareMsg 생성]
        L[View, SeqNum, Digest, NodeID]
        M[transport.Broadcast]
    end

    subgraph HandlePrepare["handlePrepare (line:452)"]
        N[뷰 번호 확인]
        O[메시지 디코딩<br/>JSON → PrepareMsg]
        P[state.AddPrepare]
        Q{IsPrepared?<br/>2f+1 Prepares}
        R[TransitionToPrepared]
        S[Phase → Prepared]
    end

    A --> B --> C --> D
    D -->|PrePrepare| E
    E --> F --> G --> H --> I --> J --> K
    K --> L --> M

    D -->|Prepare| N
    N --> O --> P --> Q
    Q -->|Yes| R --> S
    Q -->|No| P

    style Q fill:#fff9c4
    style S fill:#c8e6c9
```

**Prepare 쿼럼 확인:**

```mermaid
flowchart LR
    subgraph "State.IsPrepared(quorum)"
        A["PrepareCount()"]
        B["len(prepares) >= quorum"]
        C{">= 2f+1?"}
        D["true"]
        E["false"]
    end

    subgraph "QuorumSize 계산"
        F["n = len(validators)"]
        G["f = (n-1)/3"]
        H["quorum = 2f+1"]
        I["4노드: 2*1+1=3"]
    end

    A --> B --> C
    C -->|Yes| D
    C -->|No| E

    F --> G --> H --> I
```

---

### 2.4 Phase 4: Commit (확정)

```mermaid
flowchart TB
    subgraph AfterPrepared["PREPARED 상태 진입 후"]
        A[TransitionToPrepared]
        B[CommitMsg 생성]
        C[View, SeqNum, Digest, NodeID]
        D[transport.Broadcast]
    end

    subgraph HandleCommit["handleCommit (line:500)"]
        E[뷰 번호 확인]
        F[메시지 디코딩<br/>JSON → CommitMsg]
        G[state.AddCommit]
        H{IsCommitted?<br/>2f+1 Commits}
        I[TransitionToCommitted]
        J[Phase → Committed]
    end

    subgraph ExecuteBlock["executeBlock (line:539)"]
        K{state.Executed?}
        L[app.ExecuteBlock]
        M[ABCI FinalizeBlock]
        N[app.Commit]
        O[상태 영구 저장]
        P[state.MarkExecuted]
    end

    subgraph MempoolUpdate["Mempool 업데이트"]
        Q{mempool != nil?}
        R[committedTxs 수집]
        S[mempool.Update]
        T[txStore에서 제거]
        U[recentlyRemoved에 추가]
        V[메트릭 업데이트]
    end

    subgraph Finalize["완료"]
        W[committedBlocks에 추가]
        X[메트릭 기록]
        Y[다음 라운드 준비]
    end

    A --> B --> C --> D

    E --> F --> G --> H
    H -->|No| G
    H -->|Yes| I --> J --> K

    K -->|Yes| W
    K -->|No| L --> M --> N --> O --> P

    P --> Q
    Q -->|Yes| R --> S --> T --> U --> V --> W
    Q -->|No| W

    W --> X --> Y

    style H fill:#fff9c4
    style J fill:#c8e6c9
    style S fill:#e1bee7
```

**블록 실행 상세:**

```mermaid
sequenceDiagram
    participant E as Engine
    participant A as ABCIAdapter
    participant App as Cosmos SDK App
    participant M as Mempool

    E->>E: executeBlock(state)

    Note over E,App: ABCI 2.0 FinalizeBlock
    E->>A: ExecuteBlock(block)
    A->>App: FinalizeBlock(req)
    App->>App: BeginBlock 로직
    loop 각 트랜잭션
        App->>App: DeliverTx 실행
    end
    App->>App: EndBlock 로직
    App-->>A: FinalizeBlockResponse
    A-->>E: (appHash, txResults)

    Note over E,App: ABCI Commit
    E->>A: Commit()
    A->>App: Commit()
    App->>App: 상태 영구 저장
    App-->>A: CommitResponse
    A-->>E: appHash

    Note over E,M: Mempool 정리
    E->>M: Update(height, committedTxs)
    M->>M: txStore에서 제거
    M->>M: senderIndex 업데이트
    M->>M: recentlyRemoved 추가
    M-->>E: success
```

---

## 3. View Change 프로토콜

```mermaid
sequenceDiagram
    autonumber
    participant R1 as Replica 1
    participant R2 as Replica 2
    participant NL as New Leader
    participant R3 as Replica 3

    Note over R1,R3: 리더 타임아웃 발생

    R1->>R1: onTimeout()
    R1->>R1: initiateViewChange()

    R1->>NL: VIEW-CHANGE (newView, lastSeq, checkpoints, preparedSet)
    R1->>R2: VIEW-CHANGE
    R1->>R3: VIEW-CHANGE

    R2->>R2: onTimeout()
    R2->>NL: VIEW-CHANGE
    R2->>R1: VIEW-CHANGE
    R2->>R3: VIEW-CHANGE

    R3->>R3: onTimeout()
    R3->>NL: VIEW-CHANGE
    R3->>R1: VIEW-CHANGE
    R3->>R2: VIEW-CHANGE

    Note over NL: 2f+1 VIEW-CHANGE 수집

    NL->>NL: sendNewView()
    NL->>R1: NEW-VIEW (view, viewChangeMsgs, prePrepareMsgs)
    NL->>R2: NEW-VIEW
    NL->>R3: NEW-VIEW

    Note over R1,R3: 새 뷰 진입, 미완료 요청 재처리

    R1->>R1: handleNewView()
    R2->>R2: handleNewView()
    R3->>R3: handleNewView()
```

```mermaid
flowchart TB
    subgraph Timeout["타임아웃 감지"]
        A[viewChangeTimer 만료]
        B[onTimeout]
        C[initiateViewChange]
    end

    subgraph CreateViewChange["ViewChange 생성"]
        D[newView = currentView + 1]
        E[lastSeqNum 계산]
        F[체크포인트 수집]
        G[PreparedSet 수집]
        H[ViewChangeMsg 생성]
    end

    subgraph Broadcast1["브로드캐스트"]
        I[transport.Broadcast]
        J[모든 노드에게 전송]
    end

    subgraph HandleViewChange["handleViewChange"]
        K[메시지 검증]
        L[viewChanges 맵에 추가]
        M{2f+1 수집?}
        N{I am new leader?}
    end

    subgraph SendNewView["새 리더: NewView 전송"]
        O[ViewChange 메시지 모음]
        P[미완료 PrePrepare 생성]
        Q[NewViewMsg 생성]
        R[broadcast NewView]
    end

    subgraph HandleNewView["handleNewView"]
        S[2f+1 ViewChange 검증]
        T[PrePrepare 검증]
        U[currentView = newView]
        V[상태 초기화]
        W[미완료 요청 처리]
    end

    A --> B --> C --> D
    D --> E --> F --> G --> H --> I --> J

    J --> K --> L --> M
    M -->|No| L
    M -->|Yes| N
    N -->|Yes| O --> P --> Q --> R
    N -->|No| S

    R --> S --> T --> U --> V --> W

    style M fill:#fff9c4
    style U fill:#c8e6c9
```

---

## 4. Checkpoint 프로토콜

```mermaid
flowchart TB
    subgraph Trigger["체크포인트 트리거"]
        A[블록 커밋 완료]
        B{seq % interval == 0?}
        C[createCheckpoint]
    end

    subgraph Create["체크포인트 생성"]
        D[현재 시퀀스 번호]
        E[상태 다이제스트<br/>앱 해시]
        F[CheckpointMsg 생성]
        G[transport.Broadcast]
    end

    subgraph Handle["handleCheckpoint"]
        H[다이제스트 검증]
        I[checkpoints 맵에 추가]
        J{2f+1 일치?}
        K[markStable]
    end

    subgraph GC["가비지 컬렉션"]
        L[lastStableSeq 업데이트]
        M[garbageCollect]
        N[오래된 stateLog 삭제]
        O[메모리 해제]
    end

    A --> B
    B -->|Yes| C --> D --> E --> F --> G
    B -->|No| A

    G --> H --> I --> J
    J -->|No| I
    J -->|Yes| K --> L --> M --> N --> O

    style B fill:#fff9c4
    style K fill:#c8e6c9
```

---

## 5. 합의 상태 전이 다이어그램

```mermaid
stateDiagram-v2
    [*] --> IDLE: 초기 상태

    IDLE --> PRE_PREPARED: PrePrepare 수신
    PRE_PREPARED --> PREPARED: 2f Prepares
    PREPARED --> COMMITTED: 2f+1 Commits
    COMMITTED --> EXECUTED: executeBlock()
    EXECUTED --> TX_REMOVED: Mempool.Update()
    TX_REMOVED --> IDLE: 다음 라운드

    state VIEW_CHANGE {
        [*] --> VC_WAITING: 타임아웃
        VC_WAITING --> VC_COLLECTED: 2f+1 ViewChange
        VC_COLLECTED --> NEW_VIEW: NewView 수신
        NEW_VIEW --> [*]: 뷰 전환 완료
    }

    IDLE --> VIEW_CHANGE: 타임아웃
    PRE_PREPARED --> VIEW_CHANGE: 타임아웃
    PREPARED --> VIEW_CHANGE: 타임아웃
    VIEW_CHANGE --> IDLE: 새 뷰 진입
```

**Phase 상세:**

```mermaid
flowchart LR
    subgraph Phases["PBFT Phase"]
        A["IDLE<br/>(0)"]
        B["PRE_PREPARED<br/>(1)"]
        C["PREPARED<br/>(2)"]
        D["COMMITTED<br/>(3)"]
        E["EXECUTED<br/>(4)"]
    end

    A -->|"SetPrePrepare()"| B
    B -->|"IsPrepared(2f+1)"| C
    C -->|"IsCommitted(2f+1)"| D
    D -->|"executeBlock()"| E

    style A fill:#e0e0e0
    style B fill:#fff9c4
    style C fill:#c8e6c9
    style D fill:#bbdefb
    style E fill:#e1bee7
```

---

## 6. Mempool 연동 플로우

### 6.1 트랜잭션 진입

```mermaid
flowchart TB
    subgraph External["외부"]
        A[Client]
    end

    subgraph Node["Node"]
        B[SubmitTx]
    end

    subgraph Mempool["Mempool"]
        C[AddTxWithMeta]
        D[txStore 저장]
        E[senderIndex 업데이트]
    end

    subgraph Reactor["Reactor"]
        F[newTxCh 수신]
        G[broadcastLoop]
        H[BroadcastTx]
    end

    subgraph P2P["P2P Network"]
        I[다른 노드들]
    end

    subgraph Engine["Engine"]
        J{IsPrimary?}
        K[SubmitRequest]
        L[requestChan]
    end

    A -->|"tx, clientID"| B
    B --> C --> D --> E
    E --> F --> G --> H --> I

    D --> J
    J -->|Yes| K --> L
    J -->|No| M[대기]

    style A fill:#e1f5fe
    style D fill:#c8e6c9
    style H fill:#bbdefb
```

### 6.2 블록 제안 시 트랜잭션 수집

```mermaid
flowchart TB
    subgraph Engine["Engine (리더)"]
        A[proposeBlock]
        B[mempool.ReapMaxTxs 500]
    end

    subgraph Mempool["Mempool.ReapMaxTxs()"]
        C[txStore 순회]
        D[Timestamp 기준 정렬<br/>FIFO]
        E["최대 max개 선택"]
        F["[]*Tx 반환"]
    end

    subgraph Convert["트랜잭션 변환"]
        G["mptx.ID → tx.ID"]
        H["mptx.Data → tx.Data"]
        I["mptx.Timestamp → tx.Timestamp"]
        J["mptx.Sender → tx.From"]
    end

    subgraph Block["블록 생성"]
        K[types.Transaction 목록]
        L[CreateBlock]
        M[블록 해시 계산]
    end

    A --> B --> C --> D --> E --> F
    F --> G --> H --> I --> J --> K
    K --> L --> M

    style D fill:#fff9c4
    style M fill:#c8e6c9
```

### 6.3 블록 커밋 후 정리

```mermaid
flowchart TB
    subgraph Execute["executeBlock 완료"]
        A[state.MarkExecuted]
        B[committedBlocks 추가]
    end

    subgraph Cleanup["Mempool 정리"]
        C{mempool != nil?}
        D[committedTxs 수집]
        E["tx.Data → [][]byte"]
        F[mempool.Update]
    end

    subgraph Update["Mempool.Update()"]
        G["txStore에서 삭제"]
        H["senderIndex 업데이트"]
        I["recentlyRemoved 추가"]
        J["height 업데이트"]
        K["메트릭 업데이트<br/>TxsCommitted++"]
    end

    subgraph Metrics["메트릭"]
        L[RecordBlockExecutionTime]
        M[IncrementConsensusRounds]
        N[RecordConsensusDuration]
    end

    A --> B --> C
    C -->|Yes| D --> E --> F --> G
    G --> H --> I --> J --> K
    C -->|No| L
    K --> L --> M --> N

    style F fill:#e1bee7
    style G fill:#ffcdd2
```

---

## 7. 메시지 처리 플로우

```mermaid
flowchart TB
    subgraph Network["네트워크 수신"]
        A[gRPC Transport]
        B[BroadcastMessage RPC]
        C[메시지 디코딩]
    end

    subgraph Handler["메시지 핸들러"]
        D[handleIncomingMessage]
        E[msgChan <- msg]
        F{채널 가득?}
        G[메시지 버림]
    end

    subgraph Loop["run 루프"]
        H[select]
        I[msgChan에서 수신]
        J[handleMessage]
    end

    subgraph Dispatch["타입별 분기"]
        K{msg.Type}
        L[handlePrePrepare]
        M[handlePrepare]
        N[handleCommit]
        O[handleViewChange]
        P[handleNewView]
    end

    subgraph Metrics["메트릭"]
        Q[RecordMessageProcessingTime]
        R[IncrementMessagesReceived]
    end

    A --> B --> C --> D --> E --> F
    F -->|Yes| G
    F -->|No| H

    H --> I --> J --> K
    K -->|PrePrepare| L
    K -->|Prepare| M
    K -->|Commit| N
    K -->|ViewChange| O
    K -->|NewView| P

    L --> Q --> R
    M --> Q
    N --> Q
    O --> Q
    P --> Q

    style K fill:#fff9c4
```

---

## 8. 쿼럼 요구사항

```mermaid
flowchart LR
    subgraph Nodes["노드 구성 (n=4)"]
        A["f = (n-1)/3 = 1"]
        B["최대 1개 비잔틴 허용"]
    end

    subgraph Quorums["쿼럼 요구사항"]
        C["Pre-Prepare: 1 (리더만)"]
        D["Prepare: 2f = 2"]
        E["Commit: 2f+1 = 3"]
        F["ViewChange: 2f+1 = 3"]
        G["Checkpoint: 2f+1 = 3"]
    end

    A --> C
    A --> D
    A --> E
    A --> F
    A --> G
```

| 단계 | 필요 메시지 수 | 4노드 | 7노드 | 10노드 |
|------|--------------|-------|-------|--------|
| Pre-Prepare | 1 | 1 | 1 | 1 |
| Prepare | 2f | 2 | 4 | 6 |
| Commit | 2f+1 | 3 | 5 | 7 |
| View Change | 2f+1 | 3 | 5 | 7 |
| Checkpoint | 2f+1 | 3 | 5 | 7 |

---

## 9. 주요 함수 참조

```mermaid
flowchart TB
    subgraph NodePkg["node/"]
        A["SubmitTx()<br/>node.go:340"]
    end

    subgraph MempoolPkg["mempool/"]
        B["AddTxWithMeta()<br/>mempool.go:269"]
        C["ReapMaxTxs()<br/>mempool.go"]
        D["Update()<br/>mempool.go"]
    end

    subgraph EnginePkg["consensus/pbft/"]
        E["proposeBlock()<br/>engine.go:310"]
        F["handlePrePrepare()<br/>engine.go:392"]
        G["handlePrepare()<br/>engine.go:452"]
        H["handleCommit()<br/>engine.go:500"]
        I["executeBlock()<br/>engine.go:539"]
        J["SetMempool()<br/>engine.go:211"]
    end

    subgraph TransportPkg["transport/"]
        K["Broadcast()<br/>grpc.go"]
        L["Send()<br/>grpc.go"]
    end

    A --> B
    E --> C
    I --> D

    E --> K
    F --> K
    G --> K
    H --> I

    style A fill:#e1f5fe
    style E fill:#fff9c4
    style I fill:#c8e6c9
```

---

## 10. 전체 시스템 아키텍처

```mermaid
flowchart TB
    subgraph Client["클라이언트"]
        A[Transaction]
    end

    subgraph Node["PBFT Node"]
        subgraph Components["컴포넌트"]
            B[Node]
            C[Engine]
            D[Mempool]
            E[Reactor]
            F[Transport]
            G[Metrics]
        end

        subgraph ABCI["ABCI 2.0"]
            H[ABCIAdapter]
        end
    end

    subgraph App["Cosmos SDK App"]
        I[Application]
    end

    subgraph Network["P2P Network"]
        J[Other Nodes]
    end

    subgraph Monitoring["모니터링"]
        K[Prometheus]
    end

    A -->|SubmitTx| B
    B --> D
    B --> C
    D --> E
    E --> F
    F <--> J
    C --> H
    H <--> I
    C --> G
    G --> K

    style C fill:#fff9c4
    style D fill:#c8e6c9
    style H fill:#bbdefb
```

---

## 11. 에러 처리 플로우

```mermaid
flowchart TB
    subgraph Errors["에러 유형"]
        A[ErrTxAlreadyExists<br/>중복 트랜잭션]
        B[ErrMempoolFull<br/>Mempool 가득 참]
        C[ErrTxTooLarge<br/>트랜잭션 너무 큼]
        D[ErrInvalidTx<br/>유효하지 않은 tx]
        E[ViewChange 필요<br/>리더 장애]
    end

    subgraph Handling["처리"]
        F[트랜잭션 거부]
        G[퇴출 후 재시도]
        H[클라이언트 에러 반환]
        I[View Change 시작]
    end

    A --> F --> H
    B --> G
    C --> F --> H
    D --> F --> H
    E --> I

    style A fill:#ffcdd2
    style B fill:#fff9c4
    style I fill:#bbdefb
```

---

## 12. 성능 메트릭 수집

```mermaid
flowchart LR
    subgraph Sources["메트릭 소스"]
        A[Engine<br/>합의 라운드]
        B[Mempool<br/>트랜잭션 수]
        C[Transport<br/>메시지 수]
        D[Block<br/>실행 시간]
    end

    subgraph Metrics["Prometheus 메트릭"]
        E[consensus_rounds_total]
        F[consensus_duration]
        G[mempool_size]
        H[messages_sent_total]
        I[block_execution_time]
        J[tps]
    end

    subgraph Export["내보내기"]
        K[/metrics 엔드포인트]
        L[Prometheus 스크래핑]
        M[Grafana 대시보드]
    end

    A --> E
    A --> F
    B --> G
    C --> H
    D --> I
    A --> J

    E --> K
    F --> K
    G --> K
    H --> K
    I --> K
    J --> K

    K --> L --> M
```

---

## 8. EngineV2 ABCI 2.0 + Mempool 통합 플로우

### 8.1 EngineV2 블록 제안 (proposeBlock with Mempool)

```mermaid
flowchart TB
    subgraph Trigger["블록 제안 트리거"]
        A[requestChan에서<br/>요청 수신]
        B{isPrimary?}
    end

    subgraph MempoolReap["Mempool에서 Tx 수집"]
        C[mempool != nil?]
        D[mp.ReapMaxTxs 500<br/>FIFO 순서로 최대 500개]
        E[mempoolTxs → txs 변환<br/>tx.Data 추출]
        F[단일 요청만 사용<br/>req.Operation]
    end

    subgraph ABCI["ABCI 2.0 PrepareProposal"]
        G[PrepareProposal 호출<br/>앱에게 tx 정렬/필터 요청]
        H[preparedTxs 수신<br/>앱이 선택한 최종 트랜잭션들]
    end

    subgraph BlockCreation["블록 생성"]
        I[types.NewBlock 생성<br/>height, prevHash, view, txs]
        J[PrePrepareMsg 생성]
        K[StateLog에 저장]
        L[broadcast PRE-PREPARE]
    end

    A --> B
    B -->|Yes| C
    B -->|No| Z[무시]
    C -->|Yes| D
    C -->|No| F
    D --> E
    E --> G
    F --> G
    G --> H
    H --> I
    I --> J
    J --> K
    K --> L
```

### 8.2 EngineV2 블록 검증 (ProcessProposal)

```mermaid
flowchart TB
    subgraph Receive["PRE-PREPARE 수신"]
        A[handlePrePrepare]
        B{msg.NodeID == getPrimaryID?}
        C{msg.View == currentView?}
    end

    subgraph ABCI["ABCI 2.0 ProcessProposal"]
        D[블록에서 txs 추출]
        E[ProcessProposal 호출<br/>앱에게 블록 검증 요청]
        F{accepted?}
    end

    subgraph Accept["검증 통과"]
        G[StateLog에 저장]
        H[PrepareMsg 생성]
        I[broadcast PREPARE]
        J[resetViewChangeTimer]
    end

    subgraph Reject["검증 실패"]
        K[로그 출력]
        L[무시 - PREPARE 전송 안함]
    end

    A --> B
    B -->|No| L
    B -->|Yes| C
    C -->|No| L
    C -->|Yes| D
    D --> E
    E --> F
    F -->|Yes| G
    F -->|No| K
    G --> H
    H --> I
    I --> J
    K --> L
```

### 8.3 EngineV2 블록 실행 (FinalizeBlock + Mempool Update)

```mermaid
flowchart TB
    subgraph Trigger["실행 트리거"]
        A[handleCommit]
        B{IsCommitted quorum?}
        C[executeBlock 호출]
    end

    subgraph ABCI["ABCI 2.0 FinalizeBlock"]
        D[FinalizeBlock 호출<br/>블록 내 모든 tx 실행]
        E[TxResults 수신<br/>각 tx의 성공/실패 결과]
        F[ValidatorUpdates 확인]
    end

    subgraph Commit["ABCI Commit"]
        G[Commit 호출]
        H[appHash 수신<br/>실행 후 앱 상태 해시]
        I[lastAppHash 저장]
    end

    subgraph MempoolUpdate["Mempool 업데이트"]
        J{mempool != nil?}
        K[committedTxs 수집<br/>블록 내 tx.Data들]
        L[mempool.Update 호출<br/>커밋된 tx 제거]
        M[로그: removed N txs,<br/>remaining: M]
    end

    subgraph Finalize["완료 처리"]
        N[state.MarkExecuted]
        O[committedBlocks에 추가]
        P[메트릭 기록]
        Q{CheckpointInterval?}
        R[createCheckpoint]
    end

    A --> B
    B -->|Yes| C
    C --> D
    D --> E
    E --> F
    F --> G
    G --> H
    H --> I
    I --> J
    J -->|Yes| K
    J -->|No| N
    K --> L
    L --> M
    M --> N
    N --> O
    O --> P
    P --> Q
    Q -->|Yes| R
```

### 8.4 Engine vs EngineV2 비교 다이어그램

```mermaid
graph LR
    subgraph Engine_V1["Engine (v1)"]
        A1[proposeBlock]
        B1[mempool.ReapMaxTxs]
        C1[직접 블록 생성]
        D1[validateBlock]
        E1[executeBlock]
        F1[mempool.Update]
    end

    subgraph Engine_V2["EngineV2 (v2)"]
        A2[proposeBlock]
        B2[mempool.ReapMaxTxs]
        C2[PrepareProposal<br/>ABCI 2.0]
        D2[ProcessProposal<br/>ABCI 2.0]
        E2[FinalizeBlock<br/>ABCI 2.0]
        F2[Commit<br/>ABCI 2.0]
        G2[mempool.Update]
    end

    A1 --> B1 --> C1
    C1 --> D1 --> E1 --> F1

    A2 --> B2 --> C2
    C2 --> D2 --> E2 --> F2 --> G2
```

### 8.5 EngineV2 전체 시퀀스 다이어그램

```mermaid
sequenceDiagram
    autonumber
    participant C as Client
    participant N as Node
    participant MP as Mempool
    participant E as EngineV2
    participant ABCI as ABCI App

    Note over C,ABCI: 1. 트랜잭션 제출
    C->>N: SubmitTx(tx, clientID)
    N->>MP: AddTxWithMeta(tx)
    MP-->>N: success
    N->>E: IsPrimary?

    alt is Primary
        N->>E: SubmitRequest()
    end

    Note over C,ABCI: 2. 블록 제안 (리더만)
    E->>MP: ReapMaxTxs(500)
    MP-->>E: [tx1, tx2, ...]
    E->>ABCI: PrepareProposal(txs)
    ABCI-->>E: preparedTxs
    E->>E: NewBlock(preparedTxs)
    E-->>E: broadcast PRE-PREPARE

    Note over C,ABCI: 3. 블록 검증 (모든 노드)
    E->>ABCI: ProcessProposal(block)
    ABCI-->>E: ACCEPT/REJECT
    E-->>E: broadcast PREPARE

    Note over C,ABCI: 4. Prepare → Commit
    E->>E: collect 2f+1 PREPARE
    E-->>E: broadcast COMMIT
    E->>E: collect 2f+1 COMMIT

    Note over C,ABCI: 5. 블록 실행 + Mempool 업데이트
    E->>ABCI: FinalizeBlock(block)
    ABCI-->>E: TxResults, AppHash
    E->>ABCI: Commit()
    ABCI-->>E: appHash, retainHeight
    E->>MP: Update(height, committedTxs)
    MP-->>E: (txs removed)

    Note over C,ABCI: 6. 응답
    E-->>C: Reply
```
