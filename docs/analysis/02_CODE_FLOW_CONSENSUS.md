# PBFT 합의 코드 플로우 분석

## 1. PBFT 합의 전체 흐름

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                         PBFT CONSENSUS FLOW                                  │
│                                                                              │
│   Client      Leader       Replica1      Replica2      Replica3             │
│      │           │            │             │             │                  │
│      │  Request  │            │             │             │                  │
│      │──────────►│            │             │             │                  │
│      │           │            │             │             │                  │
│      │           │ PRE-PREPARE│             │             │                  │
│      │           │───────────►│             │             │                  │
│      │           │───────────────────────────────────────►│                  │
│      │           │            │             │             │                  │
│      │           │◄──────────────PREPARE────────────────►│                  │
│      │           │            │◄───────────►│             │                  │
│      │           │            │             │◄───────────►│                  │
│      │           │            │             │             │                  │
│      │           │◄──────────────COMMIT─────────────────►│                  │
│      │           │            │◄───────────►│             │                  │
│      │           │            │             │◄───────────►│                  │
│      │           │            │             │             │                  │
│      │◄──────────│  Reply     │             │             │                  │
│      │           │            │             │             │                  │
└─────────────────────────────────────────────────────────────────────────────┘
```

## 2. 단계별 상세 코드 플로우

### 2.1 Phase 1: Request (클라이언트 → Node → Mempool)

```
┌─────────────────────────────────────────────────────────────────┐
│                    REQUEST PHASE                                 │
│                                                                  │
│  Client        Node           Mempool          Reactor           │
│     │            │               │               │               │
│     │ SubmitTx() │               │               │               │
│     │───────────►│               │               │               │
│     │            │               │               │               │
│     │            │ AddTxWithMeta │               │               │
│     │            │──────────────►│               │               │
│     │            │               │               │               │
│     │            │               │ validateTx()  │               │
│     │            │               │───────┐       │               │
│     │            │               │◄──────┘       │               │
│     │            │               │               │               │
│     │            │               │ newTxCh <- tx │               │
│     │            │               │──────────────►│               │
│     │            │               │               │               │
│     │            │               │               │ BroadcastTx() │
│     │            │               │               │──────┐        │
│     │            │               │               │◄─────┘        │
│     │            │◄──────────────│               │               │
│     │◄───────────│               │               │               │
│     │   result   │               │               │               │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

**코드 경로:**
```
1. node/node.go:340 - SubmitTx(tx, clientID)
   │
   ├── [if mempool != nil]
   │   ├── mempool.go:AddTxWithMeta(tx, clientID, nonce, gasPrice, gasLimit)
   │   │   ├── tx.go:NewTxWithMeta() - Tx 객체 생성 (Hash, ID, Timestamp)
   │   │   ├── validateTx() - 기본 검증 (크기, 중복 등)
   │   │   ├── addTxLocked() - txStore에 추가
   │   │   └── newTxCh <- tx - Reactor에게 알림
   │   │
   │   ├── [if IsPrimary()]  // 리더인 경우
   │   │   └── engine.SubmitRequest(tx, clientID) - 블록 제안 트리거
   │   │
   │   └── [else] return nil  // 팔로워는 저장만
   │
   └── [else] engine.SubmitRequest() - Mempool 없으면 직접 전달

2. mempool/reactor.go - Reactor 브로드캐스트 루프
   └── [tx <- newTxCh]
       └── broadcaster.BroadcastTx(tx.Data) - 다른 노드에게 전송
```

### 2.2 Phase 2: Pre-Prepare (리더 → 모든 노드)

```
┌─────────────────────────────────────────────────────────────────┐
│                  PRE-PREPARE PHASE                               │
│                                                                  │
│  Leader           Mempool          ABCIAdapter       Replicas    │
│     │                │                  │               │        │
│     │ ReapMaxTxs()   │                  │               │        │
│     │───────────────►│                  │               │        │
│     │                │                  │               │        │
│     │◄───────────────│                  │               │        │
│     │  []*Tx (FIFO)  │                  │               │        │
│     │                │                  │               │        │
│     │  PrepareProposal()                │               │        │
│     │──────────────────────────────────►│               │        │
│     │                │                  │               │        │
│     │◄──────────────────────────────────│               │        │
│     │    ordered txs (앱이 정렬)        │               │        │
│     │                │                  │               │        │
│     │  CreateBlock() │                  │               │        │
│     │────────┐       │                  │               │        │
│     │        │       │                  │               │        │
│     │◄───────┘       │                  │               │        │
│     │                │                  │               │        │
│     │  PrePrepare msg│                  │               │        │
│     │───────────────────────────────────────────────────►       │
│     │                │                  │               │        │
└─────────────────────────────────────────────────────────────────┘
```

**코드 경로:**
```
1. engine.go:310 - proposeBlock(req *RequestMsg) (리더 역할)
   │
   ├── e.sequenceNum++ - 블록 높이 증가
   │
   ├── [if mempool != nil]
   │   └── mempool.ReapMaxTxs(500) - FIFO 순서로 최대 500개 트랜잭션 수집
   │       └── mempool.go:ReapMaxTxs()
   │           └── FIFO 정렬 (Timestamp 기준)
   │           └── 반환: []*Tx
   │
   ├── [else if app != nil]
   │   └── app.GetPendingTransactions() - 하위 호환성
   │
   ├── [if req != nil]
   │   └── txs에 요청 트랜잭션 추가
   │
   ├── CreateBlock() - 블록 생성
   │   ├── 헤더 생성 (Height, Timestamp, ProposerID, View, SequenceNum)
   │   ├── 트랜잭션 포함
   │   └── 블록 해시 계산
   │
   └── broadcastPrePrepare(block)
       ├── PrePrepareMsg 생성 (View, SequenceNum, Digest, Block, PrimaryID)
       └── transport.Broadcast(PrePrepareMsg)
```

**ABCI 2.0 설계 (PrepareProposal):**
- Mempool은 FIFO로 트랜잭션 반환 (단순성)
- 실제 정렬/필터링은 `PrepareProposal`에서 앱이 담당 (유연성)
- 현재 구현: PrepareProposal 미사용 (향후 ABCI 연동 시 추가)

### 2.3 Phase 3: Prepare (모든 노드 간)

```
┌─────────────────────────────────────────────────────────────────┐
│                    PREPARE PHASE                                 │
│                                                                  │
│  Replica          ABCIAdapter          Other Replicas           │
│     │                  │                     │                   │
│     │  Receive PrePrepare                    │                   │
│     │◄──────────────────────────────────────│                   │
│     │                  │                     │                   │
│     │  ProcessProposal()                     │                   │
│     │─────────────────►│                     │                   │
│     │                  │                     │                   │
│     │◄─────────────────│                     │                   │
│     │  ACCEPT/REJECT   │                     │                   │
│     │                  │                     │                   │
│     │  [if ACCEPT]     │                     │                   │
│     │  stateLog.Add()  │                     │                   │
│     │────────┐         │                     │                   │
│     │◄───────┘         │                     │                   │
│     │                  │                     │                   │
│     │  Prepare msg     │                     │                   │
│     │─────────────────────────────────────────►                 │
│     │                  │                     │                   │
│     │◄───────────────────────────────────────│                   │
│     │  Prepare msgs from others              │                   │
│     │                  │                     │                   │
│     │  [if 2f+1 Prepares]                    │                   │
│     │  TransitionToPrepared()                │                   │
│     │                  │                     │                   │
└─────────────────────────────────────────────────────────────────┘
```

**코드 경로:**
```
1. engine.go:392 - handlePrePrepare(msg)
   │
   ├── 뷰 번호 확인 (msg.View == currentView)
   │
   ├── 메시지 디코딩 (json.Unmarshal → PrePrepareMsg)
   │
   ├── 리더 검증 (prePrepareMsg.PrimaryID == getPrimaryID())
   │
   ├── 블록 다이제스트 검증 (block.ComputeHash() == msg.Digest)
   │
   ├── [TODO: ProcessProposal() - ABCI 검증]
   │
   ├── stateLog.GetOrCreateState(seqNum)
   │   └── 상태 생성 또는 기존 상태 반환
   │
   ├── state.SetPrePrepare(&prePrepareMsg)
   │   └── 상태에 PrePrepare 저장, Phase → PrePrepared
   │
   └── broadcastPrepare()
       ├── PrepareMsg 생성 (View, SequenceNum, Digest, NodeID)
       └── transport.Broadcast(PrepareMsg)

2. engine.go:452 - handlePrepare(msg)
   │
   ├── 뷰 번호 확인
   │
   ├── 메시지 디코딩 (json.Unmarshal → PrepareMsg)
   │
   ├── state.AddPrepare(&prepareMsg)
   │   └── prepares 맵에 추가
   │
   └── [if state.IsPrepared(quorum) && phase == PrePrepared]
       ├── state.TransitionToPrepared()
       │   └── Phase → Prepared
       │
       └── broadcastCommit()
           ├── CommitMsg 생성 (View, SequenceNum, Digest, NodeID)
           └── transport.Broadcast(CommitMsg)
```

### 2.4 Phase 4: Commit (모든 노드 간)

```
┌─────────────────────────────────────────────────────────────────┐
│                    COMMIT PHASE                                  │
│                                                                  │
│  Node           Other Nodes      ABCIAdapter       Mempool      │
│     │               │                 │               │          │
│     │ [PREPARED]    │                 │               │          │
│     │ Commit msg    │                 │               │          │
│     │──────────────►│                 │               │          │
│     │               │                 │               │          │
│     │◄──────────────│                 │               │          │
│     │ Commit msgs   │                 │               │          │
│     │               │                 │               │          │
│     │ [2f+1 Commits]│                 │               │          │
│     │               │                 │               │          │
│     │ FinalizeBlock()                 │               │          │
│     │────────────────────────────────►│               │          │
│     │               │                 │               │          │
│     │◄────────────────────────────────│               │          │
│     │ execution result                │               │          │
│     │               │                 │               │          │
│     │ Commit()      │                 │               │          │
│     │────────────────────────────────►│               │          │
│     │               │                 │               │          │
│     │◄────────────────────────────────│               │          │
│     │ appHash       │                 │               │          │
│     │               │                 │               │          │
│     │ Update(committedTxs)            │               │          │
│     │────────────────────────────────────────────────►│          │
│     │               │                 │               │          │
│     │◄────────────────────────────────────────────────│          │
│     │ txs removed   │                 │               │          │
│     │               │                 │               │          │
└─────────────────────────────────────────────────────────────────┘
```

**코드 경로:**
```
1. engine.go:500 - handleCommit(msg)
   │
   ├── 뷰 번호 확인
   │
   ├── 메시지 디코딩 (json.Unmarshal → CommitMsg)
   │
   ├── state.AddCommit(&commitMsg)
   │   └── commits 맵에 추가
   │
   └── [if state.IsCommitted(quorum) && phase == Prepared]
       ├── state.TransitionToCommitted()
       │   └── Phase → Committed
       │
       └── executeBlock(state)

2. engine.go:539 - executeBlock(state)
   │
   ├── [if state.Executed || state.Block == nil] return
   │
   ├── [if app != nil]
   │   ├── app.ExecuteBlock(state.Block)
   │   │   └── ABCI FinalizeBlock (BeginBlock + DeliverTx + EndBlock 통합)
   │   │       - 각 트랜잭션 실행
   │   │       - 상태 변경 적용
   │   │       - 이벤트 수집
   │   │       - 반환: TxResults, ValidatorUpdates, AppHash
   │   │
   │   └── app.Commit(state.Block)
   │       └── ABCI Commit
   │           - 상태 영구 저장
   │           - 반환: appHash
   │
   ├── state.MarkExecuted()
   │   └── Executed = true
   │
   ├── committedBlocks에 블록 추가
   │
   ├── [if mempool != nil] ★ Mempool 업데이트
   │   └── mempool.Update(height, committedTxs)
   │       ├── 커밋된 트랜잭션 txStore에서 제거
   │       ├── senderIndex 업데이트
   │       ├── recentlyRemoved 캐시에 추가 (재진입 방지)
   │       └── 메트릭 업데이트 (TxsCommitted++)
   │
   └── 메트릭 업데이트
       ├── RecordBlockExecutionTime()
       ├── IncrementConsensusRounds()
       └── RecordConsensusDuration()
```

## 3. View Change 프로토콜

```
┌─────────────────────────────────────────────────────────────────┐
│                   VIEW CHANGE FLOW                               │
│                                                                  │
│  Replica           New Leader          Other Replicas           │
│     │                   │                     │                  │
│     │  [Timeout]        │                     │                  │
│     │  ViewChange msg   │                     │                  │
│     │──────────────────►│                     │                  │
│     │───────────────────────────────────────►│                  │
│     │                   │                     │                  │
│     │                   │◄────────────────────│                  │
│     │                   │  ViewChange msgs    │                  │
│     │                   │                     │                  │
│     │                   │  [2f+1 ViewChanges] │                  │
│     │                   │                     │                  │
│     │◄──────────────────│                     │                  │
│     │  NewView msg      │────────────────────►│                  │
│     │                   │                     │                  │
│     │  [Enter new view] │                     │                  │
│     │                   │                     │                  │
└─────────────────────────────────────────────────────────────────┘
```

**코드 경로:**
```
1. 타임아웃 발생
   view_change.go - OnViewChangeTimeout()
   └── StartViewChange(currentView + 1)
       ├── 새 뷰 번호 계산 (currentView + 1)
       ├── ViewChangeMsg 생성
       │   ├── NewView: currentView + 1
       │   ├── LastSeqNum: 마지막 안정 시퀀스
       │   ├── Checkpoints: 체크포인트 증명
       │   └── PreparedSet: 준비된 인증서
       └── broadcast(ViewChangeMsg)

2. ViewChange 수신
   engine.go - handleViewChange(msg)
   └── viewChangeManager.HandleViewChange(msg)
       ├── 메시지 검증
       ├── viewChanges 맵에 추가
       └── [if 2f+1 ViewChanges && I am new leader]
           └── BroadcastNewView()

3. NewView 전송 (새 리더)
   view_change.go - BroadcastNewView()
   ├── 수집된 ViewChange 메시지 포함
   ├── 미완료 요청에 대한 PrePrepare 생성
   └── broadcast(NewViewMsg)

4. NewView 수신
   engine.go - handleNewView(msg)
   └── viewChangeManager.HandleNewView(msg)
       ├── 2f+1 ViewChange 증명 검증
       ├── PrePrepare 메시지 검증
       ├── 뷰 전환
       │   ├── currentView = msg.View
       │   └── 상태 초기화
       └── 미완료 요청 처리
```

## 4. Checkpoint 프로토콜

```
┌─────────────────────────────────────────────────────────────────┐
│                   CHECKPOINT FLOW                                │
│                                                                  │
│  Node              Other Nodes                                   │
│     │                   │                                        │
│     │  [Block committed]│                                        │
│     │  [seq % interval == 0]                                     │
│     │                   │                                        │
│     │  Checkpoint msg   │                                        │
│     │──────────────────►│                                        │
│     │                   │                                        │
│     │◄──────────────────│                                        │
│     │  Checkpoint msgs  │                                        │
│     │                   │                                        │
│     │  [2f+1 matching]  │                                        │
│     │  Mark stable      │                                        │
│     │                   │                                        │
│     │  Garbage collect  │                                        │
│     │  old logs         │                                        │
│     │                   │                                        │
└─────────────────────────────────────────────────────────────────┘
```

**코드 경로:**
```
1. 체크포인트 생성
   engine.go - afterBlockCommit()
   └── [if sequence % checkpointInterval == 0]
       └── createCheckpoint()
           ├── Checkpoint 메시지 생성
           │   ├── Sequence: 현재 시퀀스
           │   └── Digest: 상태 다이제스트 (앱 해시)
           └── broadcast(CheckpointMsg)

2. 체크포인트 수신
   engine.go - handleCheckpoint(msg)
   ├── 다이제스트 검증
   ├── checkpoints 맵에 추가
   └── checkStableCheckpoint()
       └── [if 2f+1 matching checkpoints]
           └── markStable()
               ├── lastStableSeq 업데이트
               └── garbageCollect()
                   └── 오래된 로그 정리

3. 가비지 컬렉션
   state.go - garbageCollect()
   ├── stateLog에서 old entries 삭제
   └── 메모리 해제
```

## 5. 합의 상태 전이

```
┌─────────────────────────────────────────────────────────────────┐
│                    STATE TRANSITIONS                             │
│                                                                  │
│   ┌─────────┐    PrePrepare    ┌──────────────┐                 │
│   │  IDLE   │ ────────────────►│ PRE-PREPARED │                 │
│   └─────────┘                  └──────────────┘                 │
│        ▲                              │                          │
│        │                              │ 2f Prepares              │
│        │                              ▼                          │
│        │                       ┌──────────────┐                 │
│        │                       │   PREPARED   │                 │
│        │                       └──────────────┘                 │
│        │                              │                          │
│        │                              │ 2f+1 Commits             │
│        │                              ▼                          │
│        │                       ┌──────────────┐                 │
│        │◄──────────────────────│  COMMITTED   │                 │
│        │      Next Round       └──────────────┘                 │
│                                       │                          │
│                                       │ executeBlock()           │
│                                       ▼                          │
│                                ┌──────────────┐                 │
│                                │   EXECUTED   │                 │
│                                └──────────────┘                 │
│                                       │                          │
│                                       │ Mempool.Update()         │
│                                       ▼                          │
│                                ┌──────────────┐                 │
│                                │  TX REMOVED  │                 │
│                                └──────────────┘                 │
│                                                                  │
│   ┌─────────────────────────────────────────────────────────┐   │
│   │                    VIEW CHANGE                           │   │
│   │                                                          │   │
│   │   Any State ──[Timeout]──► VIEW_CHANGE ──[2f+1]──►      │   │
│   │                                           NEW_VIEW       │   │
│   └─────────────────────────────────────────────────────────┘   │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

## 6. Mempool 연동 플로우

```
┌─────────────────────────────────────────────────────────────────┐
│                   MEMPOOL INTEGRATION                            │
│                                                                  │
│   ┌─────────────────────────────────────────────────────────┐   │
│   │  트랜잭션 진입                                            │   │
│   │                                                          │   │
│   │   Client ──► Node.SubmitTx() ──► Mempool.AddTxWithMeta() │   │
│   │                     │                    │                │   │
│   │                     │                    ▼                │   │
│   │                     │             Reactor.newTxCh         │   │
│   │                     │                    │                │   │
│   │                     ▼                    ▼                │   │
│   │              [if IsPrimary]       BroadcastTx()           │   │
│   │              Engine.SubmitRequest()    (P2P 전파)         │   │
│   └─────────────────────────────────────────────────────────┘   │
│                                                                  │
│   ┌─────────────────────────────────────────────────────────┐   │
│   │  블록 제안 (리더)                                         │   │
│   │                                                          │   │
│   │   Engine.proposeBlock()                                  │   │
│   │         │                                                │   │
│   │         ▼                                                │   │
│   │   Mempool.ReapMaxTxs(500)  ──► FIFO 순서로 500개 수집    │   │
│   │         │                                                │   │
│   │         ▼                                                │   │
│   │   CreateBlock(txs)                                       │   │
│   │         │                                                │   │
│   │         ▼                                                │   │
│   │   BroadcastPrePrepare()                                  │   │
│   └─────────────────────────────────────────────────────────┘   │
│                                                                  │
│   ┌─────────────────────────────────────────────────────────┐   │
│   │  블록 커밋 후                                             │   │
│   │                                                          │   │
│   │   Engine.executeBlock()                                  │   │
│   │         │                                                │   │
│   │         ▼                                                │   │
│   │   app.ExecuteBlock() + app.Commit()                      │   │
│   │         │                                                │   │
│   │         ▼                                                │   │
│   │   Mempool.Update(height, committedTxs)                   │   │
│   │         │                                                │   │
│   │         ├──► 커밋된 tx 제거 (txStore, senderIndex)       │   │
│   │         ├──► recentlyRemoved 캐시에 추가                 │   │
│   │         └──► 메트릭 업데이트                              │   │
│   └─────────────────────────────────────────────────────────┘   │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

## 7. 쿼럼 요구사항

| 단계 | 필요 메시지 수 | 설명 |
|------|--------------|------|
| Pre-Prepare | 1 (리더) | 리더만 제안 |
| Prepare | 2f | 자신 포함 2f개 |
| Commit | 2f+1 | 자신 포함 2f+1개 |
| View Change | 2f+1 | 뷰 변경 동의 |
| Checkpoint | 2f+1 | 안정적 체크포인트 |

**f = (n-1)/3** (n은 총 노드 수)
- 4 노드: f=1, 2f+1=3
- 7 노드: f=2, 2f+1=5
- 10 노드: f=3, 2f+1=7

## 8. 주요 함수 참조

| 함수 | 파일:라인 | 설명 |
|------|----------|------|
| `Node.SubmitTx()` | node/node.go:340 | 트랜잭션 제출 진입점 |
| `Mempool.AddTxWithMeta()` | mempool/mempool.go | 트랜잭션 추가 |
| `Mempool.ReapMaxTxs()` | mempool/mempool.go | FIFO로 tx 수집 |
| `Mempool.Update()` | mempool/mempool.go | 커밋된 tx 제거 |
| `Engine.proposeBlock()` | consensus/pbft/engine.go:310 | 블록 제안 (리더) |
| `Engine.handlePrePrepare()` | consensus/pbft/engine.go:392 | Pre-Prepare 처리 |
| `Engine.handlePrepare()` | consensus/pbft/engine.go:452 | Prepare 처리 |
| `Engine.handleCommit()` | consensus/pbft/engine.go:500 | Commit 처리 |
| `Engine.executeBlock()` | consensus/pbft/engine.go:539 | 블록 실행 |
| `Engine.SetMempool()` | consensus/pbft/engine.go:211 | Mempool 연결 |
