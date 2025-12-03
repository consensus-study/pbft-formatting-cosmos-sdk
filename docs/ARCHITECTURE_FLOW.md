# PBFT-Cosmos 아키텍처 및 코드 플로우

## 목차
1. [시스템 아키텍처](#1-시스템-아키텍처)
2. [노드 시작 플로우](#2-노드-시작-플로우)
3. [트랜잭션 제출 플로우](#3-트랜잭션-제출-플로우)
4. [PBFT 합의 플로우](#4-pbft-합의-플로우)
5. [블록 실행 및 커밋 플로우](#5-블록-실행-및-커밋-플로우)
6. [뷰 체인지 플로우](#6-뷰-체인지-플로우)
7. [Mempool 플로우](#7-mempool-플로우)
8. [네트워크 통신 플로우](#8-네트워크-통신-플로우)
9. [ABCI 2.0 통합 플로우](#9-abci-20-통합-플로우)
10. [메트릭 수집 플로우](#10-메트릭-수집-플로우)

---

## 1. 시스템 아키텍처

### 1.1 전체 시스템 구조

```mermaid
graph TB
    subgraph "PBFT Node"
        subgraph "Application Layer"
            CLI[CLI - cmd/pbftd]
            API[API Endpoints]
        end

        subgraph "Node Layer"
            Node[Node Manager]
            Config[Configuration]
        end

        subgraph "Consensus Layer"
            Engine[PBFT Engine]
            StateLog[State Log]
            VCM[ViewChange Manager]
        end

        subgraph "Mempool Layer"
            Mempool[Mempool]
            Reactor[Reactor]
            TxStore[Tx Store]
        end

        subgraph "Network Layer"
            Transport[gRPC Transport]
            PeerMgr[Peer Manager]
        end

        subgraph "ABCI Layer"
            ABCIAdapter[ABCI Adapter]
            ABCIClient[ABCI Client]
        end

        subgraph "Metrics Layer"
            Metrics[Prometheus Metrics]
            MetricsSrv[Metrics Server]
        end
    end

    subgraph "External"
        Peers[Other PBFT Nodes]
        ABCIApp[Cosmos SDK App]
        PromServer[Prometheus Server]
    end

    CLI --> Node
    API --> Node
    Node --> Engine
    Node --> Mempool
    Node --> Transport
    Node --> Metrics

    Engine --> StateLog
    Engine --> VCM
    Engine --> Mempool
    Engine --> Transport
    Engine --> ABCIAdapter

    Mempool --> TxStore
    Mempool --> Reactor
    Reactor --> Transport

    Transport --> PeerMgr
    Transport <--> Peers

    ABCIAdapter --> ABCIClient
    ABCIClient <--> ABCIApp

    Metrics --> MetricsSrv
    MetricsSrv --> PromServer
```

### 1.2 패키지 의존성

```mermaid
graph LR
    subgraph "cmd/"
        pbftd[pbftd/main.go]
    end

    subgraph "node/"
        node[node.go]
        config[config.go]
    end

    subgraph "consensus/pbft/"
        engine[engine.go]
        state[state.go]
        messages[messages.go]
        view_change[view_change.go]
        abci_adapter[abci_adapter.go]
        engine_v2[engine_v2.go]
    end

    subgraph "mempool/"
        mempool[mempool.go]
        reactor[reactor.go]
        tx[tx.go]
    end

    subgraph "transport/"
        grpc[grpc.go]
    end

    subgraph "abci/"
        client[client.go]
        types[types.go]
    end

    subgraph "types/"
        types_pkg[types.go]
    end

    subgraph "metrics/"
        prometheus[prometheus.go]
    end

    pbftd --> node
    node --> engine
    node --> mempool
    node --> grpc
    node --> prometheus

    engine --> state
    engine --> messages
    engine --> view_change
    engine --> mempool
    engine --> grpc
    engine --> abci_adapter

    abci_adapter --> client
    client --> types

    mempool --> tx
    mempool --> reactor
    reactor --> grpc

    engine --> types_pkg
    mempool --> types_pkg
```

---

## 2. 노드 시작 플로우

### 2.1 CLI에서 노드 시작

```mermaid
sequenceDiagram
    participant User
    participant CLI as cmd/pbftd
    participant Viper as Viper Config
    participant Node as node.Node
    participant Transport as GRPCTransport
    participant Mempool as Mempool
    participant Reactor as Reactor
    participant Engine as PBFT Engine
    participant Metrics as Metrics Server

    User->>CLI: pbftd start --node-id=node0
    CLI->>Viper: Load config file
    Viper-->>CLI: Config loaded

    CLI->>CLI: Build node.Config
    CLI->>Node: NewNode(config)

    activate Node
    Node->>Node: config.Validate()
    Node->>Transport: NewGRPCTransport(nodeID, listenAddr)
    Transport-->>Node: transport

    Node->>Node: types.NewValidatorSet(validators)
    Node->>Mempool: NewMempool(mempoolConfig)
    Mempool-->>Node: mempool

    Node->>Reactor: NewReactor(mempool, reactorConfig)
    Reactor-->>Node: reactor

    Node->>Engine: NewEngine(pbftConfig, validatorSet, transport, app, metrics)
    Engine-->>Node: engine

    Node->>Engine: SetMempool(mempool)
    deactivate Node

    Node-->>CLI: node created

    CLI->>Node: Start(ctx)
    activate Node

    Node->>Transport: Start()
    Transport->>Transport: net.Listen("tcp", address)
    Transport->>Transport: grpc.NewServer()
    Transport->>Transport: go server.Serve()
    Transport-->>Node: started

    loop For each peer
        Node->>Transport: AddPeer(peerID, peerAddr)
        Transport->>Transport: grpc.DialContext()
        Transport-->>Node: connected
    end

    Node->>Mempool: Start()
    Mempool->>Mempool: go expireLoop()
    Mempool->>Mempool: go cleanupCacheLoop()
    Mempool-->>Node: started

    Node->>Reactor: SetBroadcaster(transport)
    Node->>Reactor: Start()
    Reactor->>Reactor: go broadcastLoop()
    Reactor-->>Node: started

    Node->>Metrics: go startMetricsServer()
    Metrics->>Metrics: http.ListenAndServe()

    Node->>Engine: Start()
    Engine->>Engine: go run()
    Engine->>Engine: resetViewChangeTimer()
    Engine-->>Node: started

    deactivate Node

    Node-->>CLI: Node started successfully
    CLI-->>User: Print startup info

    Note over CLI: Wait for SIGINT/SIGTERM
```

### 2.2 노드 내부 초기화 상세

```mermaid
flowchart TD
    subgraph "NewNode(config)"
        A[config.Validate] --> B[Create Transport]
        B --> C[Create ValidatorSet]
        C --> D[Create pbftConfig]
        D --> E[Create Metrics]
        E --> F[Create Mempool]
        F --> G[Create Reactor]
        G --> H[Create Engine]
        H --> I[engine.SetMempool]
        I --> J[Return Node]
    end

    subgraph "Node.Start(ctx)"
        K[transport.Start] --> L[Connect to Peers]
        L --> M[mempool.Start]
        M --> N[reactor.SetBroadcaster]
        N --> O[reactor.Start]
        O --> P[startMetricsServer]
        P --> Q[engine.Start]
        Q --> R[Log startup info]
    end

    J --> K
```

---

## 3. 트랜잭션 제출 플로우

### 3.1 클라이언트 트랜잭션 제출

```mermaid
sequenceDiagram
    participant Client
    participant Node as node.Node
    participant Mempool as Mempool
    participant Engine as PBFT Engine
    participant Reactor as Reactor
    participant Transport as Transport
    participant OtherNodes as Other Nodes

    Client->>Node: SubmitTx(txBytes, clientID)

    alt Mempool exists
        Node->>Mempool: AddTxWithMeta(tx, sender, nonce, gasPrice, gasLimit)

        activate Mempool
        Mempool->>Mempool: Check size limit
        Mempool->>Mempool: Create Tx object with hash
        Mempool->>Mempool: Check duplicate (txStore)
        Mempool->>Mempool: Check recentlyRemoved
        Mempool->>Mempool: Check minGasPrice
        Mempool->>Mempool: checkNonce(sender, nonce)

        alt CheckTxCallback set
            Mempool->>Mempool: checkTxCallback(tx)
        end

        Mempool->>Mempool: ensureCapacity(tx)

        alt Mempool full & lower priority exists
            Mempool->>Mempool: evictLowestPriority()
        end

        Mempool->>Mempool: addTxLocked(tx)
        Note over Mempool: txStore[hash] = tx<br/>senderIndex[sender] = append(tx)<br/>Update metrics

        Mempool->>Reactor: newTxCh <- tx
        deactivate Mempool

        Reactor->>Reactor: Add to broadcast queue
        Reactor->>Transport: BroadcastTx(txBytes)
        Transport->>OtherNodes: Send tx to all peers

        Mempool-->>Node: nil (success)

        alt Node is Primary
            Node->>Engine: SubmitRequest(tx, clientID)
            Engine->>Engine: requestChan <- RequestMsg
        end

    else Mempool not exists
        Node->>Engine: SubmitRequest(tx, clientID)
        Engine->>Engine: requestChan <- RequestMsg
    end

    Node-->>Client: nil or error
```

### 3.2 Mempool 트랜잭션 저장 구조

```mermaid
flowchart TB
    subgraph "Mempool Storage"
        subgraph "txStore (map[string]*Tx)"
            TX1[hash1 -> Tx1]
            TX2[hash2 -> Tx2]
            TX3[hash3 -> Tx3]
            TX4[hash4 -> Tx4]
        end

        subgraph "senderIndex (map[string][]*Tx)"
            SA[senderA -> Tx1, Tx3]
            SB[senderB -> Tx2, Tx4]
        end

        subgraph "State"
            Count[txCount: 4]
            Bytes[txBytes: 1024]
            Height[height: 100]
        end
    end

    subgraph "Tx Structure"
        TxObj[Tx]
        TxObj --> Hash[Hash: []byte]
        TxObj --> ID[ID: string]
        TxObj --> Data[Data: []byte]
        TxObj --> Sender[Sender: string]
        TxObj --> Nonce[Nonce: uint64]
        TxObj --> GasPrice[GasPrice: uint64]
        TxObj --> Timestamp[Timestamp: time.Time]
    end
```

---

## 4. PBFT 합의 플로우

### 4.1 전체 PBFT 합의 프로토콜

```mermaid
sequenceDiagram
    participant Client
    participant Primary as Primary Node
    participant Replica1 as Replica 1
    participant Replica2 as Replica 2
    participant Replica3 as Replica 3

    Note over Primary,Replica3: Phase 0: REQUEST
    Client->>Primary: Request (Transaction)

    Note over Primary,Replica3: Phase 1: PRE-PREPARE
    Primary->>Primary: Create Block from Mempool
    Primary->>Primary: Assign sequence number
    Primary->>Replica1: PRE-PREPARE(view, seq, block)
    Primary->>Replica2: PRE-PREPARE(view, seq, block)
    Primary->>Replica3: PRE-PREPARE(view, seq, block)

    Note over Primary,Replica3: Phase 2: PREPARE
    Replica1->>Replica1: Validate PRE-PREPARE
    Replica2->>Replica2: Validate PRE-PREPARE
    Replica3->>Replica3: Validate PRE-PREPARE

    Replica1->>Primary: PREPARE(view, seq, digest)
    Replica1->>Replica2: PREPARE(view, seq, digest)
    Replica1->>Replica3: PREPARE(view, seq, digest)

    Replica2->>Primary: PREPARE(view, seq, digest)
    Replica2->>Replica1: PREPARE(view, seq, digest)
    Replica2->>Replica3: PREPARE(view, seq, digest)

    Replica3->>Primary: PREPARE(view, seq, digest)
    Replica3->>Replica1: PREPARE(view, seq, digest)
    Replica3->>Replica2: PREPARE(view, seq, digest)

    Note over Primary,Replica3: Wait for 2f+1 PREPARE messages

    Note over Primary,Replica3: Phase 3: COMMIT
    Primary->>Replica1: COMMIT(view, seq, digest)
    Primary->>Replica2: COMMIT(view, seq, digest)
    Primary->>Replica3: COMMIT(view, seq, digest)

    Replica1->>Primary: COMMIT(view, seq, digest)
    Replica1->>Replica2: COMMIT(view, seq, digest)
    Replica1->>Replica3: COMMIT(view, seq, digest)

    Replica2->>Primary: COMMIT(view, seq, digest)
    Replica2->>Replica1: COMMIT(view, seq, digest)
    Replica2->>Replica3: COMMIT(view, seq, digest)

    Replica3->>Primary: COMMIT(view, seq, digest)
    Replica3->>Replica1: COMMIT(view, seq, digest)
    Replica3->>Replica2: COMMIT(view, seq, digest)

    Note over Primary,Replica3: Wait for 2f+1 COMMIT messages

    Note over Primary,Replica3: Phase 4: EXECUTE & REPLY
    Primary->>Primary: Execute Block
    Replica1->>Replica1: Execute Block
    Replica2->>Replica2: Execute Block
    Replica3->>Replica3: Execute Block

    Primary->>Client: REPLY(result)
```

### 4.2 Engine 내부 메시지 처리

```mermaid
flowchart TD
    subgraph "Engine.run() - Main Loop"
        Start[Select on channels]
        Start --> |ctx.Done| Exit[Return - Shutdown]
        Start --> |msgChan| HandleMsg[handleMessage]
        Start --> |requestChan| HandleReq[proposeBlock - Primary only]
    end

    subgraph "handleMessage(msg)"
        HM[Switch msg.Type]
        HM --> |PrePrepare| HPP[handlePrePrepare]
        HM --> |Prepare| HP[handlePrepare]
        HM --> |Commit| HC[handleCommit]
        HM --> |ViewChange| HVC[handleViewChange]
        HM --> |NewView| HNV[handleNewView]
    end

    subgraph "handlePrePrepare"
        PP1[Verify sender is Primary]
        PP1 --> PP2[Check view number]
        PP2 --> PP3[Check window]
        PP3 --> PP4[Decode PrePrepareMsg]
        PP4 --> PP5[Validate Block]
        PP5 --> PP6[Save to StateLog]
        PP6 --> PP7[Broadcast PREPARE]
        PP7 --> PP8[Reset ViewChange timer]
    end

    subgraph "handlePrepare"
        P1[Check view number]
        P1 --> P2[Decode PrepareMsg]
        P2 --> P3[Get State from log]
        P3 --> P4[Verify digest]
        P4 --> P5[AddPrepare to state]
        P5 --> P6{IsPrepared & phase==PrePrepared?}
        P6 --> |Yes| P7[Transition to Prepared]
        P7 --> P8[Broadcast COMMIT]
        P6 --> |No| P9[Wait for more]
    end

    subgraph "handleCommit"
        C1[Check view number]
        C1 --> C2[Decode CommitMsg]
        C2 --> C3[Get State from log]
        C3 --> C4[AddCommit to state]
        C4 --> C5{IsCommitted & phase==Prepared?}
        C5 --> |Yes| C6[Transition to Committed]
        C6 --> C7[executeBlock]
        C5 --> |No| C8[Wait for more]
    end

    HandleMsg --> HM
    HandleReq --> ProposeBlock

    subgraph "proposeBlock"
        PB1[Increment sequenceNum]
        PB1 --> PB2[Check window]
        PB2 --> PB3[Collect txs from Mempool]
        PB3 --> PB4[Create Block]
        PB4 --> PB5[Validate Block]
        PB5 --> PB6[Create PrePrepareMsg]
        PB6 --> PB7[Save to StateLog]
        PB7 --> PB8[Broadcast PRE-PREPARE]
    end
```

### 4.3 상태 전이 다이어그램

```mermaid
stateDiagram-v2
    [*] --> Idle: Initial State

    Idle --> PrePrepared: Receive PRE-PREPARE<br/>(valid from Primary)

    PrePrepared --> Prepared: Receive 2f+1 PREPARE<br/>(matching digest)

    Prepared --> Committed: Receive 2f+1 COMMIT<br/>(matching digest)

    Committed --> Executed: Execute Block<br/>Update Mempool

    Executed --> [*]: Block finalized

    note right of PrePrepared
        State saved in StateLog
        Block validated
        PREPARE sent
    end note

    note right of Prepared
        Quorum reached
        COMMIT sent
    end note

    note right of Committed
        Quorum reached
        Ready for execution
    end note

    note right of Executed
        Block executed via ABCI
        Mempool updated
        Checkpoint if interval
    end note
```

### 4.4 StateLog 구조

```mermaid
flowchart TB
    subgraph "StateLog"
        subgraph "Window Management"
            LW[LowWaterMark: 100]
            HW[HighWaterMark: 300]
            WS[WindowSize: 200]
        end

        subgraph "states map[uint64]*State"
            S101[101 -> State]
            S102[102 -> State]
            S103[103 -> State]
            SDOT[...]
            S200[200 -> State]
        end
    end

    subgraph "State Structure"
        State[State]
        State --> View[View: uint64]
        State --> SeqNum[SequenceNum: uint64]
        State --> Phase[Phase: StatePhase]
        State --> PPMsg[PrePrepareMsg]
        State --> Block[Block]
        State --> Prepares[PrepareMsgs map]
        State --> Commits[CommitMsgs map]
        State --> Executed[Executed: bool]
    end

    subgraph "StatePhase"
        Idle2[Idle = 0]
        PrePrepared2[PrePrepared = 1]
        Prepared2[Prepared = 2]
        Committed2[Committed = 3]
    end
```

---

## 5. 블록 실행 및 커밋 플로우

### 5.1 블록 실행 상세

```mermaid
sequenceDiagram
    participant Engine as PBFT Engine
    participant State as State
    participant App as Application
    participant Mempool as Mempool
    participant Metrics as Metrics
    participant Logger as Logger

    Engine->>State: Check Executed flag

    alt Already executed
        Engine-->>Engine: Return (skip)
    end

    Engine->>Engine: startTime = time.Now()

    alt Application exists
        Engine->>App: ExecuteBlock(block)
        App->>App: Process transactions
        App-->>Engine: appHash, nil

        Engine->>App: Commit(block)
        App->>App: Persist state
        App-->>Engine: nil
    end

    Engine->>State: MarkExecuted()

    Engine->>Engine: Lock mutex
    Engine->>Engine: Append to committedBlocks
    Engine->>Engine: Get mempool reference
    Engine->>Engine: Unlock mutex

    alt Mempool exists
        Engine->>Engine: Extract tx data from block
        Engine->>Mempool: Update(height, committedTxs)

        activate Mempool
        Mempool->>Mempool: Update height
        Mempool->>Mempool: Update LastBlockTime

        loop For each committed tx
            Mempool->>Mempool: removeTxLocked(txID, true)
            Note over Mempool: Remove from txStore<br/>Remove from senderIndex<br/>Add to recentlyRemoved
            Mempool->>Mempool: Increment TxsCommitted
        end

        alt RecheckEnabled
            Mempool->>Mempool: recheckTxsLocked()
            Note over Mempool: Re-validate remaining txs<br/>Remove invalid ones
        end
        deactivate Mempool

        Mempool-->>Engine: nil
        Engine->>Logger: Log mempool update
    end

    alt Metrics exists
        Engine->>Metrics: EndConsensusRound(seqNum)
        Engine->>Metrics: SetBlockHeight(seqNum)
        Engine->>Metrics: RecordBlockExecutionTime(duration)
        Engine->>Metrics: AddTransactions(txCount)
    end

    Engine->>Logger: Log block execution

    alt seqNum % CheckpointInterval == 0
        Engine->>Engine: createCheckpoint(seqNum)
    end
```

### 5.2 체크포인트 생성

```mermaid
flowchart TD
    subgraph "createCheckpoint(seqNum)"
        A[Lock mutex]
        A --> B{committedBlocks exists?}
        B --> |Yes| C[Get last block hash]
        C --> D[checkpoints[seqNum] = hash]
        B --> |No| D

        D --> E{len(checkpoints) > 3?}
        E --> |Yes| F[Find oldest checkpoint]
        F --> G[Delete oldest]
        G --> H[Unlock mutex]
        E --> |No| H

        H --> I[stateLog.AdvanceWatermarks]
        I --> J[Log checkpoint created]
    end

    subgraph "AdvanceWatermarks"
        K[Update LowWaterMark]
        K --> L[Remove old states]
        L --> M[Update HighWaterMark]
    end
```

---

## 6. 뷰 체인지 플로우

### 6.1 뷰 체인지 트리거

```mermaid
sequenceDiagram
    participant Timer as ViewChange Timer
    participant Engine as PBFT Engine
    participant VCM as ViewChange Manager
    participant Transport as Transport
    participant OtherNodes as Other Nodes
    participant Metrics as Metrics

    Note over Timer,Engine: Timeout occurs (no progress)
    Timer->>Engine: Timer callback

    Engine->>Engine: startViewChange()

    activate Engine
    Engine->>Engine: Lock mutex
    Engine->>Engine: newView = view + 1
    Engine->>Engine: lastSeqNum = sequenceNum
    Engine->>Engine: Unlock mutex

    Engine->>Engine: collectCheckpoints()
    Engine->>Engine: collectPreparedCertificates()

    alt Metrics exists
        Engine->>Metrics: IncrementViewChanges()
    end

    Engine->>VCM: StartViewChange(newView, lastSeqNum, checkpoints, preparedSet)
    deactivate Engine

    activate VCM
    VCM->>VCM: Create ViewChangeMsg
    VCM->>VCM: Add to viewChangeMsgs[newView]
    VCM->>Transport: Broadcast(VIEW-CHANGE msg)
    Transport->>OtherNodes: Send to all peers
    deactivate VCM

    Engine->>Engine: Reset timer with longer timeout
    Note over Engine: Exponential backoff
```

### 6.2 뷰 체인지 메시지 처리

```mermaid
sequenceDiagram
    participant OtherNode as Other Node
    participant Transport as Transport
    participant Engine as PBFT Engine
    participant VCM as ViewChange Manager

    OtherNode->>Transport: VIEW-CHANGE message
    Transport->>Engine: handleIncomingMessage
    Engine->>Engine: msgChan <- msg
    Engine->>Engine: handleMessage(msg)
    Engine->>Engine: handleViewChange(msg)

    activate Engine
    Engine->>Engine: Decode ViewChangeMsg
    Engine->>Engine: Check newView > currentView

    alt newView <= currentView
        Engine-->>Engine: Ignore (old view)
    end

    Engine->>VCM: HandleViewChange(viewChangeMsg)
    deactivate Engine

    activate VCM
    VCM->>VCM: Add to viewChangeMsgs[newView]
    VCM->>VCM: Check quorum (2f+1)

    alt Has quorum
        VCM-->>Engine: true (has quorum)

        Engine->>Engine: Calculate new primary

        alt I am new primary
            Engine->>Engine: broadcastNewView(newView)

            activate Engine
            Engine->>VCM: CreateNewViewMsg(newView, numValidators)
            VCM-->>Engine: NewViewMsg
            Engine->>Transport: Broadcast(NEW-VIEW msg)
            Engine->>VCM: HandleNewView(newViewMsg)
            deactivate Engine
        end
    else No quorum yet
        VCM-->>Engine: false
    end
    deactivate VCM
```

### 6.3 NEW-VIEW 메시지 처리

```mermaid
sequenceDiagram
    participant NewPrimary as New Primary
    participant Transport as Transport
    participant Engine as PBFT Engine
    participant VCM as ViewChange Manager
    participant StateLog as StateLog

    NewPrimary->>Transport: NEW-VIEW message
    Transport->>Engine: handleIncomingMessage
    Engine->>Engine: handleNewView(msg)

    activate Engine
    Engine->>Engine: Decode NewViewMsg
    Engine->>Engine: Check newView > currentView
    Engine->>Engine: Verify sender is expected primary

    Engine->>VCM: HandleNewView(newViewMsg)

    alt Valid NEW-VIEW
        VCM->>VCM: Verify ViewChange messages
        VCM->>VCM: Update view
        VCM->>Engine: Call onViewChangeComplete callback

        Engine->>Engine: Update view number
        Engine->>Engine: Reset timer

        loop For each PrePrepare in NewViewMsg
            Engine->>Engine: reprocessPrePrepare(prePrepare, newView)
            Engine->>StateLog: GetState(newView, seqNum)
            Engine->>StateLog: SetPrePrepare
            Engine->>Transport: Broadcast PREPARE
        end

        VCM-->>Engine: true
    else Invalid NEW-VIEW
        VCM-->>Engine: false
    end
    deactivate Engine
```

### 6.4 뷰 체인지 상태 다이어그램

```mermaid
stateDiagram-v2
    [*] --> Normal: Node started

    Normal --> ViewChangeStarted: Timeout<br/>(no progress)

    ViewChangeStarted --> WaitingForQuorum: Broadcast VIEW-CHANGE

    WaitingForQuorum --> WaitingForQuorum: Receive VIEW-CHANGE<br/>(count < 2f+1)

    WaitingForQuorum --> QuorumReached: Receive VIEW-CHANGE<br/>(count >= 2f+1)

    QuorumReached --> BroadcastNewView: I am new primary
    QuorumReached --> WaitingForNewView: I am not primary

    BroadcastNewView --> Normal: NEW-VIEW sent & processed

    WaitingForNewView --> Normal: Receive valid NEW-VIEW
    WaitingForNewView --> ViewChangeStarted: Timeout<br/>(try next view)

    note right of ViewChangeStarted
        Collect checkpoints
        Collect prepared certificates
        Send VIEW-CHANGE
    end note

    note right of QuorumReached
        2f+1 VIEW-CHANGE messages
        for same new view
    end note
```

---

## 7. Mempool 플로우

### 7.1 트랜잭션 라이프사이클

```mermaid
flowchart TD
    subgraph "Transaction Lifecycle"
        A[Client submits tx] --> B[Node.SubmitTx]
        B --> C[Mempool.AddTxWithMeta]

        C --> D{Validation}
        D --> |Size check| E{Too large?}
        E --> |Yes| F[Reject: ErrTxTooLarge]
        E --> |No| G{Duplicate?}

        G --> |Yes| H[Reject: ErrTxAlreadyExists]
        G --> |No| I{Min gas price?}

        I --> |Below| J[Reject: ErrInsufficientGas]
        I --> |OK| K{Nonce valid?}

        K --> |No| L[Reject: ErrLowNonce]
        K --> |OK| M{CheckTx callback?}

        M --> |Fail| N[Reject: ErrInvalidTx]
        M --> |Pass| O{Capacity?}

        O --> |Full| P{Can evict?}
        P --> |No| Q[Reject: ErrMempoolFull]
        P --> |Yes| R[Evict lowest priority]
        R --> S[Add to mempool]
        O --> |OK| S

        S --> T[Store in txStore]
        T --> U[Index by sender]
        U --> V[Notify reactor]
        V --> W[Broadcast to peers]

        W --> X[Wait in mempool]
        X --> Y[Primary collects for block]
        Y --> Z[Block committed]
        Z --> AA[Mempool.Update]
        AA --> AB[Remove from mempool]
    end
```

### 7.2 ReapMaxTxs 플로우

```mermaid
sequenceDiagram
    participant Engine as PBFT Engine
    participant Mempool as Mempool
    participant TxStore as txStore

    Engine->>Mempool: ReapMaxTxs(500)

    activate Mempool
    Mempool->>Mempool: RLock()

    Mempool->>Mempool: Validate max (0 < max <= MaxBatchTxs)

    alt txCount == 0
        Mempool-->>Engine: nil
    end

    Mempool->>TxStore: Collect all txs

    Mempool->>Mempool: Sort by Timestamp (FIFO)
    Note over Mempool: ABCI 2.0 Philosophy:<br/>Mempool returns FIFO order<br/>App handles sorting in PrepareProposal

    Mempool->>Mempool: Slice to max count

    Mempool->>Mempool: RUnlock()
    deactivate Mempool

    Mempool-->>Engine: []*Tx (FIFO ordered)

    Engine->>Engine: Convert to types.Transaction
    Engine->>Engine: Create Block
```

### 7.3 Mempool Update 플로우

```mermaid
flowchart TD
    subgraph "Mempool.Update(height, committedTxs)"
        A[Lock mutex]
        A --> B[Update height]
        B --> C[Update LastBlockTime]

        C --> D[Loop: committedTxs]
        D --> E[Create Tx from bytes]
        E --> F[removeTxLocked]

        subgraph "removeTxLocked"
            F --> G[Get tx from txStore]
            G --> H[Delete from txStore]
            H --> I[Decrement txCount]
            I --> J[Decrement txBytes]
            J --> K[Remove from senderIndex]
            K --> L[Add to recentlyRemoved]
            L --> M[Update metrics]
        end

        M --> N[Increment TxsCommitted]
        N --> O{More txs?}
        O --> |Yes| D
        O --> |No| P{RecheckEnabled?}

        P --> |Yes| Q[recheckTxsLocked]

        subgraph "recheckTxsLocked"
            Q --> R[Loop all remaining txs]
            R --> S[Call checkTxCallback]
            S --> T{Valid?}
            T --> |No| U[Add to toRemove]
            T --> |Yes| V{More txs?}
            U --> V
            V --> |Yes| R
            V --> |No| W[Remove invalid txs]
        end

        P --> |No| X[Unlock mutex]
        W --> X
        X --> Y[Return nil]
    end
```

### 7.4 백그라운드 작업

```mermaid
flowchart LR
    subgraph "Mempool Background Goroutines"
        subgraph "expireLoop"
            A[Ticker: TTL/2]
            A --> B[expireTxs]
            B --> C[Find expired txs]
            C --> D[Remove expired]
            D --> E[Update TxsExpired]
            E --> A
        end

        subgraph "cleanupCacheLoop"
            F[Ticker: 5 min]
            F --> G[cleanupCache]
            G --> H[Remove old from recentlyRemoved]
            H --> I{Size > CacheSize?}
            I --> |Yes| J[Remove oldest entries]
            J --> F
            I --> |No| F
        end
    end

    subgraph "Reactor broadcastLoop"
        K[Wait on broadcastQueue]
        K --> L[Batch txs]
        L --> M[broadcaster.BroadcastTx]
        M --> K
    end
```

---

## 8. 네트워크 통신 플로우

### 8.1 gRPC Transport 구조

```mermaid
flowchart TB
    subgraph "GRPCTransport"
        subgraph "Server Side"
            Server[grpc.Server]
            Listener[net.Listener]
            Service[PBFTServiceServer]
        end

        subgraph "Client Side"
            Peers[peers map]
            PC1[peerConn: node1]
            PC2[peerConn: node2]
            PC3[peerConn: node3]
        end

        subgraph "Handlers"
            MsgHandler[msgHandler func]
            StateProvider[stateProvider]
        end
    end

    Server --> Service
    Listener --> Server

    Peers --> PC1
    Peers --> PC2
    Peers --> PC3

    Service --> MsgHandler
    Service --> StateProvider
```

### 8.2 메시지 브로드캐스트 플로우

```mermaid
sequenceDiagram
    participant Engine as PBFT Engine
    participant Transport as GRPCTransport
    participant Peer1 as Peer Node 1
    participant Peer2 as Peer Node 2
    participant Peer3 as Peer Node 3

    Engine->>Transport: Broadcast(msg)

    activate Transport
    Transport->>Transport: RLock peers
    Transport->>Transport: Copy peer list
    Transport->>Transport: RUnlock

    Transport->>Transport: messageToProto(msg)

    par Parallel broadcast
        Transport->>Peer1: BroadcastMessage(request)
        Transport->>Peer2: BroadcastMessage(request)
        Transport->>Peer3: BroadcastMessage(request)
    end

    Peer1-->>Transport: BroadcastResponse
    Peer2-->>Transport: BroadcastResponse
    Peer3-->>Transport: BroadcastResponse

    Transport->>Transport: Collect errors
    deactivate Transport

    Transport-->>Engine: lastErr or nil
```

### 8.3 메시지 수신 플로우

```mermaid
sequenceDiagram
    participant RemoteNode as Remote Node
    participant Server as gRPC Server
    participant Transport as GRPCTransport
    participant Handler as msgHandler
    participant Engine as PBFT Engine

    RemoteNode->>Server: BroadcastMessage(request)
    Server->>Transport: BroadcastMessage(ctx, req)

    activate Transport
    Transport->>Transport: Check msgHandler exists
    Transport->>Transport: protoToMessage(req.Message)
    Transport->>Handler: msgHandler(msg)
    deactivate Transport

    Handler->>Engine: handleIncomingMessage(msg)

    activate Engine
    Engine->>Engine: msgChan <- msg
    Note over Engine: Non-blocking send<br/>Drop if channel full
    deactivate Engine

    Transport-->>Server: BroadcastResponse{Success: true}
    Server-->>RemoteNode: Response
```

### 8.4 상태 동기화 플로우

```mermaid
sequenceDiagram
    participant NewNode as New Node
    participant Transport as GRPCTransport
    participant StateProvider as StateProvider (Engine)
    participant ExistingNode as Existing Node

    NewNode->>Transport: SyncState(fromHeight)

    activate Transport
    Transport->>Transport: RLock stateProvider
    Transport->>StateProvider: GetBlocksFromHeight(fromHeight)
    StateProvider-->>Transport: []*types.Block

    Transport->>StateProvider: GetCheckpoints()
    StateProvider-->>Transport: []Checkpoint
    Transport->>Transport: RUnlock

    Transport->>Transport: Convert to proto
    deactivate Transport

    Transport-->>NewNode: SyncStateResponse{Blocks, Checkpoints}

    NewNode->>NewNode: Apply blocks
    NewNode->>NewNode: Update checkpoints
```

---

## 9. ABCI 2.0 통합 플로우

### 9.1 ABCI 메서드 호출 흐름

```mermaid
flowchart TD
    subgraph "Consensus Flow with ABCI 2.0"
        A[New Block Proposal] --> B[PrepareProposal]
        B --> C[Broadcast PRE-PREPARE]
        C --> D[Receive PRE-PREPARE]
        D --> E[ProcessProposal]
        E --> F{Accept?}
        F --> |No| G[Reject Block]
        F --> |Yes| H[Continue PBFT]
        H --> I[Prepare Phase]
        I --> J[Commit Phase]
        J --> K[FinalizeBlock]
        K --> L[Commit]
        L --> M[Block Finalized]
    end
```

### 9.2 PrepareProposal 상세

```mermaid
sequenceDiagram
    participant Engine as PBFT Engine
    participant Adapter as ABCIAdapter
    participant Client as ABCI Client
    participant App as Cosmos SDK App

    Note over Engine: Primary proposing block

    Engine->>Engine: Collect txs from Mempool
    Engine->>Adapter: PrepareProposal(ctx, height, proposer, txs)

    activate Adapter
    Adapter->>Adapter: Create PrepareProposalRequest
    Note over Adapter: txs, maxTxBytes, height,<br/>time, proposerAddr

    Adapter->>Client: PrepareProposal(ctx, req)
    Client->>App: gRPC PrepareProposal

    App->>App: Filter/Reorder txs
    App->>App: Apply MEV protection
    App->>App: Check tx validity

    App-->>Client: PrepareProposalResponse{Txs}
    Client-->>Adapter: response
    deactivate Adapter

    Adapter-->>Engine: filteredTxs, nil

    Engine->>Engine: Create block with filtered txs
```

### 9.3 ProcessProposal 상세

```mermaid
sequenceDiagram
    participant Engine as PBFT Engine
    participant Adapter as ABCIAdapter
    participant Client as ABCI Client
    participant App as Cosmos SDK App

    Note over Engine: Replica received PRE-PREPARE

    Engine->>Adapter: ProcessProposal(ctx, height, proposer, txs, hash)

    activate Adapter
    Adapter->>Adapter: Create ProcessProposalRequest

    Adapter->>Client: ProcessProposal(ctx, req)
    Client->>App: gRPC ProcessProposal

    App->>App: Validate block structure
    App->>App: Verify txs are valid
    App->>App: Check proposer

    alt Valid Block
        App-->>Client: ACCEPT
    else Invalid Block
        App-->>Client: REJECT
    end

    Client-->>Adapter: response
    deactivate Adapter

    Adapter-->>Engine: accepted (bool), nil

    alt Accepted
        Engine->>Engine: Continue with PREPARE
    else Rejected
        Engine->>Engine: Ignore block
    end
```

### 9.4 FinalizeBlock 상세

```mermaid
sequenceDiagram
    participant Engine as PBFT Engine
    participant Adapter as ABCIAdapter
    participant Client as ABCI Client
    participant App as Cosmos SDK App

    Note over Engine: Block committed (2f+1 COMMITs)

    Engine->>Adapter: FinalizeBlock(ctx, block)

    activate Adapter
    Adapter->>Adapter: Extract txs from block
    Adapter->>Adapter: Create BlockData
    Adapter->>Adapter: Create FinalizeBlockRequest

    Adapter->>Client: FinalizeBlock(ctx, req)
    Client->>App: gRPC FinalizeBlock

    App->>App: BeginBlock logic

    loop For each tx
        App->>App: DeliverTx logic
        App->>App: Execute state changes
        App->>App: Collect events
    end

    App->>App: EndBlock logic
    App->>App: Validator updates
    App->>App: Consensus param updates

    App-->>Client: FinalizeBlockResponse
    Note over Client: TxResults, ValidatorUpdates,<br/>ConsensusParamUpdates, AppHash, Events

    Client-->>Adapter: response
    Adapter->>Adapter: Convert to ABCIExecutionResult
    deactivate Adapter

    Adapter-->>Engine: result, nil

    Engine->>Engine: Handle validator updates
    Engine->>Engine: Store app hash
```

### 9.5 Commit 플로우

```mermaid
sequenceDiagram
    participant Engine as PBFT Engine
    participant Adapter as ABCIAdapter
    participant Client as ABCI Client
    participant App as Cosmos SDK App

    Engine->>Adapter: Commit(ctx)

    activate Adapter
    Adapter->>Client: Commit(ctx)
    Client->>App: gRPC Commit

    App->>App: Persist state to disk
    App->>App: Calculate app hash
    App->>App: Cleanup

    App-->>Client: CommitResponse{RetainHeight}
    Client-->>Adapter: response

    Adapter->>Adapter: Increment lastHeight
    deactivate Adapter

    Adapter-->>Engine: appHash, retainHeight, nil
```

---

## 10. 메트릭 수집 플로우

### 10.1 메트릭 구조

```mermaid
flowchart TB
    subgraph "Metrics Collection Points"
        subgraph "Consensus Metrics"
            CR[consensusRoundsTotal]
            CD[consensusDuration]
            BH[currentBlockHeight]
            CV[currentView]
            VC[viewChangesTotal]
        end

        subgraph "Message Metrics"
            MS[messagesSentTotal]
            MR[messagesReceivedTotal]
            MP[messageProcessingTime]
        end

        subgraph "Block Metrics"
            BE[blockExecutionTime]
            TT[transactionsTotal]
            TPS[tps]
        end
    end

    subgraph "Collection Sources"
        Engine[PBFT Engine]
        Transport[Transport]
        Node[Node]
    end

    Engine --> CR
    Engine --> CD
    Engine --> BH
    Engine --> CV
    Engine --> VC
    Engine --> BE
    Engine --> TT
    Engine --> TPS
    Engine --> MS
    Engine --> MR
    Engine --> MP
```

### 10.2 메트릭 수집 타이밍

```mermaid
sequenceDiagram
    participant Engine as PBFT Engine
    participant Metrics as Metrics
    participant Prometheus as Prometheus Server

    Note over Engine: Consensus Round Start
    Engine->>Metrics: StartConsensusRound(seqNum)
    Metrics->>Metrics: roundStartTimes[seqNum] = now

    Note over Engine: Message Sent
    Engine->>Metrics: IncrementMessagesSent(msgType)

    Note over Engine: Message Received
    Engine->>Metrics: IncrementMessagesReceived(msgType)
    Engine->>Metrics: RecordMessageProcessingTime(msgType, duration)

    Note over Engine: View Change
    Engine->>Metrics: IncrementViewChanges()
    Engine->>Metrics: SetCurrentView(newView)

    Note over Engine: Block Executed
    Engine->>Metrics: EndConsensusRound(seqNum)
    Metrics->>Metrics: Calculate duration
    Metrics->>Metrics: consensusDuration.Observe()
    Metrics->>Metrics: consensusRoundsTotal.Inc()

    Engine->>Metrics: SetBlockHeight(height)
    Engine->>Metrics: RecordBlockExecutionTime(duration)
    Engine->>Metrics: AddTransactions(count)

    Prometheus->>Metrics: GET /metrics
    Metrics-->>Prometheus: Prometheus format metrics
```

### 10.3 Prometheus Exposition

```mermaid
flowchart TD
    subgraph "Metrics Server"
        HTTP[HTTP Server :26660]

        subgraph "Endpoints"
            ME[/metrics]
            HE[/health]
            SE[/status]
        end
    end

    subgraph "Prometheus Format Output"
        M1["pbft_consensus_rounds_total 150"]
        M2["pbft_consensus_duration_seconds{quantile='0.5'} 0.5"]
        M3["pbft_block_height 1500"]
        M4["pbft_current_view 3"]
        M5["pbft_messages_sent_total{type='PrePrepare'} 150"]
        M6["pbft_messages_received_total{type='Prepare'} 450"]
        M7["pbft_view_changes_total 3"]
        M8["pbft_tps 100"]
    end

    HTTP --> ME
    HTTP --> HE
    HTTP --> SE

    ME --> M1
    ME --> M2
    ME --> M3
    ME --> M4
    ME --> M5
    ME --> M6
    ME --> M7
    ME --> M8
```

---

## 부록: 타입 참조

### 핵심 타입

```mermaid
classDiagram
    class Node {
        +Config config
        +Engine engine
        +GRPCTransport transport
        +Mempool mempool
        +Reactor reactor
        +Metrics metrics
        +Start(ctx) error
        +Stop() error
        +SubmitTx(tx, clientID) error
    }

    class Engine {
        +Config config
        +uint64 view
        +uint64 sequenceNum
        +ValidatorSet validatorSet
        +StateLog stateLog
        +Transport transport
        +Mempool mempool
        +Start() error
        +Stop()
        +SubmitRequest(op, clientID) error
        +IsPrimary() bool
    }

    class Mempool {
        +Config config
        +map txStore
        +map senderIndex
        +Start() error
        +Stop() error
        +AddTx(tx) error
        +ReapMaxTxs(max) []*Tx
        +Update(height, txs) error
    }

    class GRPCTransport {
        +string nodeID
        +grpc.Server server
        +map peers
        +Start() error
        +Stop()
        +Broadcast(msg) error
        +Send(nodeID, msg) error
    }

    class State {
        +uint64 View
        +uint64 SequenceNum
        +StatePhase Phase
        +PrePrepareMsg PrePrepareMsg
        +Block Block
        +map PrepareMsgs
        +map CommitMsgs
        +IsPrepared(quorum) bool
        +IsCommitted(quorum) bool
    }

    Node --> Engine
    Node --> Mempool
    Node --> GRPCTransport
    Engine --> State
    Engine --> Mempool
    Engine --> GRPCTransport
```

### 메시지 타입

```mermaid
classDiagram
    class Message {
        +MessageType Type
        +uint64 View
        +uint64 SequenceNum
        +[]byte Digest
        +string NodeID
        +time.Time Timestamp
        +[]byte Signature
        +[]byte Payload
    }

    class PrePrepareMsg {
        +uint64 View
        +uint64 SequenceNum
        +[]byte Digest
        +Block Block
        +string PrimaryID
    }

    class PrepareMsg {
        +uint64 View
        +uint64 SequenceNum
        +[]byte Digest
        +string NodeID
    }

    class CommitMsg {
        +uint64 View
        +uint64 SequenceNum
        +[]byte Digest
        +string NodeID
    }

    class ViewChangeMsg {
        +uint64 NewView
        +uint64 LastSeqNum
        +[]Checkpoint Checkpoints
        +[]PreparedCert PreparedSet
        +string NodeID
    }

    class NewViewMsg {
        +uint64 View
        +[]ViewChangeMsg ViewChangeMsgs
        +[]PrePrepareMsg PrePrepareMsgs
        +string NewPrimaryID
    }

    Message <|-- PrePrepareMsg
    Message <|-- PrepareMsg
    Message <|-- CommitMsg
    Message <|-- ViewChangeMsg
    Message <|-- NewViewMsg
```
