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

### 2.1 Phase 1: Request (클라이언트 → 리더)

```
┌─────────────────────────────────────────────────────────────────┐
│                    REQUEST PHASE                                 │
│                                                                  │
│  Client                   Reactor                  Mempool       │
│     │                        │                        │          │
│     │  SubmitTx(txBytes)     │                        │          │
│     │───────────────────────►│                        │          │
│     │                        │                        │          │
│     │                        │  AddTxWithMeta()       │          │
│     │                        │───────────────────────►│          │
│     │                        │                        │          │
│     │                        │    CheckTx (ABCI)      │          │
│     │                        │    ◄──────────────────►│          │
│     │                        │                        │          │
│     │                        │◄───────────────────────│          │
│     │                        │    success/error       │          │
│     │                        │                        │          │
│     │                        │  broadcastQueue <- tx  │          │
│     │                        │───────┐                │          │
│     │                        │       │                │          │
│     │                        │◄──────┘                │          │
│     │                        │                        │          │
│     │◄───────────────────────│                        │          │
│     │      result            │                        │          │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

**코드 경로:**
```
1. reactor.go:187 - SubmitTx()
   └── reactor.go:192 - SubmitTxWithMeta()
       ├── mempool.go:AddTxWithMeta()
       │   ├── NewTxWithMeta() - Tx 객체 생성
       │   ├── validateTx() - 기본 검증
       │   ├── checkTxCallback() - ABCI CheckTx 호출
       │   └── addTxLocked() - 멤풀에 추가
       └── broadcastQueue <- tx - 브로드캐스트 큐에 추가
```

### 2.2 Phase 2: Pre-Prepare (리더 → 모든 노드)

```
┌─────────────────────────────────────────────────────────────────┐
│                  PRE-PREPARE PHASE                               │
│                                                                  │
│  Leader              ABCIAdapter            Replicas             │
│     │                     │                     │                │
│     │  CollectTxs()       │                     │                │
│     │────────┐            │                     │                │
│     │        │            │                     │                │
│     │◄───────┘            │                     │                │
│     │                     │                     │                │
│     │  PrepareProposal()  │                     │                │
│     │────────────────────►│                     │                │
│     │                     │                     │                │
│     │◄────────────────────│                     │                │
│     │    ordered txs      │                     │                │
│     │                     │                     │                │
│     │  CreateBlock()      │                     │                │
│     │────────┐            │                     │                │
│     │        │            │                     │                │
│     │◄───────┘            │                     │                │
│     │                     │                     │                │
│     │  PrePrepare msg     │                     │                │
│     │──────────────────────────────────────────►│                │
│     │                     │                     │                │
└─────────────────────────────────────────────────────────────────┘
```

**코드 경로:**
```
1. engine.go - proposeBlock() (리더 역할)
   ├── mempool.ReapMaxTxs() - 트랜잭션 수집
   │
   ├── abci_adapter.go:92 - PrepareProposal()
   │   └── client.PrepareProposal(ctx, req)
   │       - 앱에게 트랜잭션 정렬/필터링 요청
   │       - 반환: 정렬된 트랜잭션 목록
   │
   ├── CreateBlock() - 블록 생성
   │   ├── 헤더 생성 (Height, Timestamp, ProposerID)
   │   └── 트랜잭션 포함
   │
   └── broadcastPrePrepare()
       └── transport.Broadcast(PrePrepareMsg)
```

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
│     │  Prepare msg     │                     │                   │
│     │─────────────────────────────────────────►                 │
│     │                  │                     │                   │
│     │◄───────────────────────────────────────│                   │
│     │  Prepare msgs from others              │                   │
│     │                  │                     │                   │
│     │  [if 2f+1 Prepares]                    │                   │
│     │  Enter PREPARED state                  │                   │
│     │                  │                     │                   │
└─────────────────────────────────────────────────────────────────┘
```

**코드 경로:**
```
1. engine.go - handlePrePrepare(msg)
   ├── 메시지 검증
   │   ├── 뷰 번호 확인
   │   ├── 시퀀스 번호 확인
   │   └── 서명 검증
   │
   ├── abci_adapter.go:111 - ProcessProposal()
   │   └── client.ProcessProposal(ctx, req)
   │       - 앱에게 블록 검증 요청
   │       - 반환: ACCEPT 또는 REJECT
   │
   ├── [if ACCEPT]
   │   ├── 로컬 상태 저장 (prepareLog에 추가)
   │   └── broadcastPrepare()
   │       └── transport.Broadcast(PrepareMsg)
   │
   └── [if REJECT]
       └── 제안 거부, 로그 기록

2. engine.go - handlePrepare(msg)
   ├── 메시지 검증
   ├── prepareLog에 추가
   └── checkPrepared()
       └── [if 2f+1 prepares]
           └── enterPreparedState()
```

### 2.4 Phase 4: Commit (모든 노드 간)

```
┌─────────────────────────────────────────────────────────────────┐
│                    COMMIT PHASE                                  │
│                                                                  │
│  Node              Other Nodes           ABCIAdapter            │
│     │                   │                     │                  │
│     │  [PREPARED state] │                     │                  │
│     │  Commit msg       │                     │                  │
│     │──────────────────►│                     │                  │
│     │                   │                     │                  │
│     │◄──────────────────│                     │                  │
│     │  Commit msgs      │                     │                  │
│     │                   │                     │                  │
│     │  [if 2f+1 Commits]│                     │                  │
│     │                   │                     │                  │
│     │  FinalizeBlock()  │                     │                  │
│     │────────────────────────────────────────►│                  │
│     │                   │                     │                  │
│     │◄────────────────────────────────────────│                  │
│     │  execution result │                     │                  │
│     │                   │                     │                  │
│     │  Commit()         │                     │                  │
│     │────────────────────────────────────────►│                  │
│     │                   │                     │                  │
│     │◄────────────────────────────────────────│                  │
│     │  appHash          │                     │                  │
│     │                   │                     │                  │
└─────────────────────────────────────────────────────────────────┘
```

**코드 경로:**
```
1. engine.go - enterPreparedState()
   └── broadcastCommit()
       └── transport.Broadcast(CommitMsg)

2. engine.go - handleCommit(msg)
   ├── 메시지 검증
   ├── commitLog에 추가
   └── checkCommitted()
       └── [if 2f+1 commits]
           └── executeBlock()

3. engine.go - executeBlock()
   ├── abci_adapter.go:130 - FinalizeBlock()
   │   └── client.FinalizeBlock(ctx, req)
   │       - ABCI 2.0: BeginBlock + DeliverTx + EndBlock 통합
   │       - 각 트랜잭션 실행
   │       - 상태 변경 적용
   │       - 이벤트 수집
   │       - 반환: TxResults, ValidatorUpdates, AppHash
   │
   ├── abci_adapter.go:164 - Commit()
   │   └── client.Commit(ctx)
   │       - 상태 영구 저장
   │       - 반환: appHash, retainHeight
   │
   ├── mempool.RemoveTxs() - 실행된 트랜잭션 제거
   │
   └── updateState()
       ├── 블록 높이 증가
       ├── 앱 해시 업데이트
       └── 메트릭 업데이트
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
   engine.go - onTimeout()
   └── initiateViewChange()
       ├── 새 뷰 번호 계산 (currentView + 1)
       ├── ViewChangeMsg 생성
       │   ├── NewView: currentView + 1
       │   ├── LastSeq: 마지막 안정 시퀀스
       │   ├── Checkpoints: 체크포인트 증명
       │   └── PreparedSet: 준비된 인증서
       └── broadcast(ViewChangeMsg)

2. ViewChange 수신
   engine.go - handleViewChange(msg)
   ├── 메시지 검증
   ├── viewChangeLog에 추가
   └── checkViewChangeQuorum()
       └── [if 2f+1 ViewChanges]
           └── [if I am new leader]
               └── sendNewView()

3. NewView 전송 (새 리더)
   engine.go - sendNewView()
   ├── 수집된 ViewChange 메시지 포함
   ├── 미완료 요청에 대한 PrePrepare 생성
   └── broadcast(NewViewMsg)

4. NewView 수신
   engine.go - handleNewView(msg)
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
   ├── checkpointLog에 추가
   └── checkStableCheckpoint()
       └── [if 2f+1 matching checkpoints]
           └── markStable()
               ├── lastStableSeq 업데이트
               └── garbageCollect()
                   └── 오래된 로그 정리

3. 가비지 컬렉션
   engine.go - garbageCollect()
   ├── prepareLog에서 old entries 삭제
   ├── commitLog에서 old entries 삭제
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

## 6. 쿼럼 요구사항

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
