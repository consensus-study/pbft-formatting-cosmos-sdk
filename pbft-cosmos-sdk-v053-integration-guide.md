# PBFT + Cosmos SDK v0.53.0 완전 호환 구현 가이드

## 버전 정보

```
Cosmos SDK: v0.53.0 (또는 v0.53.x)
CometBFT:   v0.38.x (ABCI 2.0)
Go:         1.22+
```

## 프로젝트 목표

기존 PBFT 합의 엔진을 Cosmos SDK v0.53.0 ABCI 2.0과 완전 호환되도록 수정하여,
CometBFT 대신 PBFT를 Cosmos SDK 애플리케이션의 합의 엔진으로 사용.

---

## 1. 프로젝트 구조

```
pbft-cosmos/
├── proto/                          # Protobuf 정의
│   └── pbft/
│       └── v1/
│           ├── types.proto         # PBFT 메시지 타입
│           └── service.proto       # gRPC 서비스 정의
│
├── api/                            # 생성된 Protobuf Go 코드
│   └── pbft/
│       └── v1/
│           ├── types.pb.go
│           └── service_grpc.pb.go
│
├── abci/                           # ABCI 2.0 구현
│   ├── client.go                   # ABCI 클라이언트 (PBFT → App)
│   └── types.go                    # ABCI 타입 변환
│
├── pbft/                           # 기존 PBFT 엔진 (수정)
│   ├── engine.go                   # 메인 엔진 (수정 필요)
│   ├── message.go                  # 메시지 타입
│   ├── state.go                    # 상태 관리
│   ├── view_change.go              # 뷰 체인지
│   └── abci_adapter.go             # NEW: ABCI 어댑터
│
├── transport/                      # 네트워크 레이어
│   └── grpc.go                     # gRPC 기반 P2P
│
├── node/                           # 노드 실행
│   ├── node.go                     # 노드 구조체
│   └── config.go                   # 설정
│
├── cmd/                            # 실행 파일
│   └── pbftd/
│       └── main.go
│
├── go.mod
└── go.sum
```

---

## 2. 의존성 (go.mod)

```go
module github.com/ahwlsqja/pbft-cosmos

go 1.22

require (
    // Cosmos SDK v0.53.0 + CometBFT v0.38.x
    github.com/cosmos/cosmos-sdk v0.53.0
    github.com/cometbft/cometbft v0.38.11
    
    // Core 모듈
    cosmossdk.io/core v0.11.0
    cosmossdk.io/log v1.3.1
    cosmossdk.io/store v1.1.0
    cosmossdk.io/errors v1.0.1
    
    // gRPC
    google.golang.org/grpc v1.62.0
    google.golang.org/protobuf v1.33.0
    
    // 기타
    github.com/spf13/cobra v1.8.0
    github.com/spf13/viper v1.18.2
    github.com/cosmos/gogoproto v1.7.0
)
```

---

## 3. ABCI 2.0 인터페이스 (CometBFT v0.38.x)

### 3.1 주요 ABCI 메서드

```
┌─────────────────────────────────────────────────────────────────┐
│                    ABCI 2.0 Methods (v0.38)                     │
├─────────────────────────────────────────────────────────────────┤
│  Info              │ 앱 정보 조회                                │
│  InitChain         │ 체인 초기화                                 │
│  CheckTx           │ 트랜잭션 검증 (멤풀)                        │
│  PrepareProposal   │ 블록 제안 준비 (리더만)                     │
│  ProcessProposal   │ 블록 제안 검증 (모든 노드)                  │
│  FinalizeBlock     │ 블록 실행 (BeginBlock+DeliverTx+EndBlock)  │
│  Commit            │ 상태 커밋                                   │
│  ExtendVote        │ 투표 확장 (선택적)                          │
│  VerifyVoteExtension│ 투표 확장 검증 (선택적)                    │
│  Query             │ 상태 쿼리                                   │
└─────────────────────────────────────────────────────────────────┘
```

### 3.2 PBFT와 ABCI 2.0 메서드 매핑

```
PBFT 흐름                      ABCI 2.0 메서드
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

리더가 블록 제안 준비          → PrepareProposal
  ↓
PrePrepare 수신 시 검증        → ProcessProposal
  ↓
Prepare/Commit 투표 완료       
  ↓
블록 실행                      → FinalizeBlock
  ↓
상태 커밋                      → Commit
```

---

## 4. Protobuf 정의

### 4.1 proto/pbft/v1/types.proto

```protobuf
syntax = "proto3";

package pbft.v1;

option go_package = "github.com/ahwlsqja/pbft-cosmos/api/pbft/v1";

import "google/protobuf/timestamp.proto";

// 메시지 타입
enum MessageType {
    MESSAGE_TYPE_UNSPECIFIED = 0;
    MESSAGE_TYPE_PRE_PREPARE = 1;
    MESSAGE_TYPE_PREPARE = 2;
    MESSAGE_TYPE_COMMIT = 3;
    MESSAGE_TYPE_VIEW_CHANGE = 4;
    MESSAGE_TYPE_NEW_VIEW = 5;
    MESSAGE_TYPE_CHECKPOINT = 6;
}

// 기본 PBFT 메시지 (네트워크 전송용)
message PBFTMessage {
    MessageType type = 1;
    uint64 view = 2;
    uint64 sequence_num = 3;
    bytes digest = 4;
    string node_id = 5;
    google.protobuf.Timestamp timestamp = 6;
    bytes signature = 7;
    bytes payload = 8;  // 실제 메시지 데이터 (JSON/Protobuf)
}

// PrePrepare 메시지
message PrePrepareMsg {
    uint64 view = 1;
    uint64 sequence_num = 2;
    bytes digest = 3;
    bytes block_data = 4;  // 직렬화된 블록
    string primary_id = 5;
}

// Prepare 메시지
message PrepareMsg {
    uint64 view = 1;
    uint64 sequence_num = 2;
    bytes digest = 3;
    string node_id = 4;
}

// Commit 메시지
message CommitMsg {
    uint64 view = 1;
    uint64 sequence_num = 2;
    bytes digest = 3;
    string node_id = 4;
}

// ViewChange 메시지
message ViewChangeMsg {
    uint64 new_view = 1;
    uint64 last_seq_num = 2;
    repeated Checkpoint checkpoints = 3;
    repeated PreparedCert prepared_set = 4;
    string node_id = 5;
}

// NewView 메시지
message NewViewMsg {
    uint64 view = 1;
    repeated ViewChangeMsg view_change_msgs = 2;
    repeated PrePrepareMsg pre_prepare_msgs = 3;
    string new_primary_id = 4;
}

// 체크포인트
message Checkpoint {
    uint64 sequence_num = 1;
    bytes digest = 2;
    string node_id = 3;
}

// Prepared 인증서
message PreparedCert {
    PrePrepareMsg pre_prepare = 1;
    repeated PrepareMsg prepares = 2;
}

// 블록 헤더
message BlockHeader {
    uint64 height = 1;
    google.protobuf.Timestamp timestamp = 2;
    bytes prev_hash = 3;
    bytes tx_hash = 4;
    bytes state_hash = 5;
    string proposer = 6;
    uint64 view = 7;
}

// 블록
message Block {
    BlockHeader header = 1;
    repeated bytes txs = 2;
    bytes hash = 3;
}

// 검증자
message Validator {
    string id = 1;
    bytes pub_key = 2;
    int64 voting_power = 3;
}

// 검증자 집합
message ValidatorSet {
    repeated Validator validators = 1;
}
```

### 4.2 proto/pbft/v1/service.proto

```protobuf
syntax = "proto3";

package pbft.v1;

option go_package = "github.com/ahwlsqja/pbft-cosmos/api/pbft/v1";

import "pbft/v1/types.proto";

// PBFT 노드 간 P2P 통신 서비스
service PBFTService {
    // 메시지 브로드캐스트
    rpc BroadcastMessage(BroadcastMessageRequest) returns (BroadcastMessageResponse);
    
    // 특정 노드에 메시지 전송
    rpc SendMessage(SendMessageRequest) returns (SendMessageResponse);
    
    // 양방향 메시지 스트림
    rpc MessageStream(stream PBFTMessage) returns (stream PBFTMessage);
    
    // 상태 동기화
    rpc SyncState(SyncStateRequest) returns (SyncStateResponse);
    
    // 체크포인트 요청
    rpc GetCheckpoint(GetCheckpointRequest) returns (GetCheckpointResponse);
}

message BroadcastMessageRequest {
    PBFTMessage message = 1;
}

message BroadcastMessageResponse {
    bool success = 1;
    string error = 2;
}

message SendMessageRequest {
    string target_node_id = 1;
    PBFTMessage message = 2;
}

message SendMessageResponse {
    bool success = 1;
    string error = 2;
}

message SyncStateRequest {
    uint64 from_height = 1;
    uint64 to_height = 2;
}

message SyncStateResponse {
    repeated Block blocks = 1;
    repeated Checkpoint checkpoints = 2;
}

message GetCheckpointRequest {
    uint64 sequence_num = 1;
}

message GetCheckpointResponse {
    Checkpoint checkpoint = 1;
    bytes state_hash = 2;
}
```

---

## 5. ABCI 클라이언트 구현

### 5.1 abci/types.go

```go
package abci

import (
    "time"

    abci "github.com/cometbft/cometbft/abci/types"
    "github.com/ahwlsqja/pbft-cosmos/pbft"
)

// PBFT 블록 → ABCI RequestFinalizeBlock 변환
func BlockToFinalizeBlockRequest(block *pbft.Block, height int64) *abci.RequestFinalizeBlock {
    txs := make([][]byte, len(block.Transactions))
    for i, tx := range block.Transactions {
        txs[i] = tx.Data
    }

    return &abci.RequestFinalizeBlock{
        Txs:               txs,
        Hash:              block.Hash,
        Height:            height,
        Time:              block.Header.Timestamp,
        ProposerAddress:   []byte(block.Header.Proposer),
        DecidedLastCommit: abci.CommitInfo{},  // 필요시 채우기
        Misbehavior:       []abci.Misbehavior{},
    }
}

// ABCI ResponseFinalizeBlock → PBFT 결과 변환
func FinalizeBlockResponseToResult(resp *abci.ResponseFinalizeBlock) *ExecutionResult {
    txResults := make([]TxResult, len(resp.TxResults))
    for i, r := range resp.TxResults {
        txResults[i] = TxResult{
            Code:      r.Code,
            Data:      r.Data,
            Log:       r.Log,
            GasWanted: r.GasWanted,
            GasUsed:   r.GasUsed,
        }
    }

    return &ExecutionResult{
        TxResults:             txResults,
        ValidatorUpdates:      resp.ValidatorUpdates,
        ConsensusParamUpdates: resp.ConsensusParamUpdates,
        AppHash:               resp.AppHash,
        Events:                resp.Events,
    }
}

// PrepareProposal 요청 생성
func NewPrepareProposalRequest(
    txs [][]byte,
    maxTxBytes int64,
    height int64,
    time time.Time,
    proposer []byte,
) *abci.RequestPrepareProposal {
    return &abci.RequestPrepareProposal{
        Txs:               txs,
        MaxTxBytes:        maxTxBytes,
        Height:            height,
        Time:              time,
        ProposerAddress:   proposer,
        LocalLastCommit:   abci.ExtendedCommitInfo{},
        Misbehavior:       []abci.Misbehavior{},
    }
}

// ProcessProposal 요청 생성
func NewProcessProposalRequest(
    txs [][]byte,
    height int64,
    time time.Time,
    proposer []byte,
    hash []byte,
) *abci.RequestProcessProposal {
    return &abci.RequestProcessProposal{
        Txs:                 txs,
        Hash:                hash,
        Height:              height,
        Time:                time,
        ProposerAddress:     proposer,
        ProposedLastCommit:  abci.CommitInfo{},
        Misbehavior:         []abci.Misbehavior{},
    }
}

// 실행 결과
type ExecutionResult struct {
    TxResults             []TxResult
    ValidatorUpdates      []abci.ValidatorUpdate
    ConsensusParamUpdates *abci.ConsensusParams
    AppHash               []byte
    Events                []abci.Event
}

// 트랜잭션 결과
type TxResult struct {
    Code      uint32
    Data      []byte
    Log       string
    GasWanted int64
    GasUsed   int64
}
```

### 5.2 abci/client.go

```go
package abci

import (
    "context"
    "fmt"
    "sync"
    "time"

    abci "github.com/cometbft/cometbft/abci/types"
    "google.golang.org/grpc"
    "google.golang.org/grpc/credentials/insecure"
)

// ABCIClient - PBFT 엔진이 Cosmos SDK 앱과 통신하는 클라이언트
type ABCIClient struct {
    mu     sync.RWMutex
    conn   *grpc.ClientConn
    client abci.ABCIClient
    
    // 앱 정보 캐시
    lastHeight  int64
    lastAppHash []byte
}

// NewABCIClient 생성
func NewABCIClient(address string) (*ABCIClient, error) {
    conn, err := grpc.Dial(
        address,
        grpc.WithTransportCredentials(insecure.NewCredentials()),
        grpc.WithBlock(),
        grpc.WithTimeout(10*time.Second),
    )
    if err != nil {
        return nil, fmt.Errorf("failed to connect to ABCI app: %w", err)
    }

    return &ABCIClient{
        conn:   conn,
        client: abci.NewABCIClient(conn),
    }, nil
}

// Close 연결 종료
func (c *ABCIClient) Close() error {
    return c.conn.Close()
}

// Info - 앱 정보 조회
func (c *ABCIClient) Info(ctx context.Context) (*abci.ResponseInfo, error) {
    return c.client.Info(ctx, &abci.RequestInfo{})
}

// InitChain - 체인 초기화
func (c *ABCIClient) InitChain(ctx context.Context, req *abci.RequestInitChain) (*abci.ResponseInitChain, error) {
    return c.client.InitChain(ctx, req)
}

// CheckTx - 트랜잭션 검증 (멤풀 진입 전)
func (c *ABCIClient) CheckTx(ctx context.Context, tx []byte) (*abci.ResponseCheckTx, error) {
    return c.client.CheckTx(ctx, &abci.RequestCheckTx{
        Tx:   tx,
        Type: abci.CheckTxType_New,
    })
}

// PrepareProposal - 블록 제안 준비 (리더만 호출)
func (c *ABCIClient) PrepareProposal(ctx context.Context, req *abci.RequestPrepareProposal) (*abci.ResponsePrepareProposal, error) {
    return c.client.PrepareProposal(ctx, req)
}

// ProcessProposal - 블록 제안 검증 (모든 노드)
func (c *ABCIClient) ProcessProposal(ctx context.Context, req *abci.RequestProcessProposal) (*abci.ResponseProcessProposal, error) {
    return c.client.ProcessProposal(ctx, req)
}

// FinalizeBlock - 블록 실행 (BeginBlock + DeliverTx + EndBlock 통합)
func (c *ABCIClient) FinalizeBlock(ctx context.Context, req *abci.RequestFinalizeBlock) (*abci.ResponseFinalizeBlock, error) {
    return c.client.FinalizeBlock(ctx, req)
}

// Commit - 상태 커밋
func (c *ABCIClient) Commit(ctx context.Context) (*abci.ResponseCommit, error) {
    resp, err := c.client.Commit(ctx, &abci.RequestCommit{})
    if err != nil {
        return nil, err
    }
    
    c.mu.Lock()
    c.lastHeight++
    c.mu.Unlock()
    
    return resp, nil
}

// ExtendVote - 투표 확장 (PBFT에서는 보통 사용 안 함)
func (c *ABCIClient) ExtendVote(ctx context.Context, req *abci.RequestExtendVote) (*abci.ResponseExtendVote, error) {
    return c.client.ExtendVote(ctx, req)
}

// VerifyVoteExtension - 투표 확장 검증
func (c *ABCIClient) VerifyVoteExtension(ctx context.Context, req *abci.RequestVerifyVoteExtension) (*abci.ResponseVerifyVoteExtension, error) {
    return c.client.VerifyVoteExtension(ctx, req)
}

// Query - 상태 쿼리
func (c *ABCIClient) Query(ctx context.Context, req *abci.RequestQuery) (*abci.ResponseQuery, error) {
    return c.client.Query(ctx, req)
}

// ListSnapshots - 스냅샷 목록
func (c *ABCIClient) ListSnapshots(ctx context.Context) (*abci.ResponseListSnapshots, error) {
    return c.client.ListSnapshots(ctx, &abci.RequestListSnapshots{})
}

// OfferSnapshot - 스냅샷 제공
func (c *ABCIClient) OfferSnapshot(ctx context.Context, req *abci.RequestOfferSnapshot) (*abci.ResponseOfferSnapshot, error) {
    return c.client.OfferSnapshot(ctx, req)
}

// LoadSnapshotChunk - 스냅샷 청크 로드
func (c *ABCIClient) LoadSnapshotChunk(ctx context.Context, req *abci.RequestLoadSnapshotChunk) (*abci.ResponseLoadSnapshotChunk, error) {
    return c.client.LoadSnapshotChunk(ctx, req)
}

// ApplySnapshotChunk - 스냅샷 청크 적용
func (c *ABCIClient) ApplySnapshotChunk(ctx context.Context, req *abci.RequestApplySnapshotChunk) (*abci.ResponseApplySnapshotChunk, error) {
    return c.client.ApplySnapshotChunk(ctx, req)
}

// GetLastHeight - 마지막 높이 반환
func (c *ABCIClient) GetLastHeight() int64 {
    c.mu.RLock()
    defer c.mu.RUnlock()
    return c.lastHeight
}
```

---

## 6. PBFT-ABCI 어댑터

### 6.1 pbft/abci_adapter.go

```go
package pbft

import (
    "context"
    "fmt"
    "sync"
    "time"

    abci "github.com/cometbft/cometbft/abci/types"
    abciclient "github.com/ahwlsqja/pbft-cosmos/abci"
)

// ABCIAdapter - PBFT 엔진과 ABCI 앱 사이의 어댑터
type ABCIAdapter struct {
    mu     sync.RWMutex
    client *abciclient.ABCIClient

    // 현재 상태
    lastHeight  int64
    lastAppHash []byte

    // 설정
    maxTxBytes int64
}

// NewABCIAdapter 생성
func NewABCIAdapter(abciAddress string) (*ABCIAdapter, error) {
    client, err := abciclient.NewABCIClient(abciAddress)
    if err != nil {
        return nil, err
    }

    return &ABCIAdapter{
        client:     client,
        maxTxBytes: 1024 * 1024, // 1MB 기본값
    }, nil
}

// Close 종료
func (a *ABCIAdapter) Close() error {
    return a.client.Close()
}

// InitChain 체인 초기화
func (a *ABCIAdapter) InitChain(ctx context.Context, chainID string, validators []*Validator, appState []byte) error {
    abciValidators := make([]abci.ValidatorUpdate, len(validators))
    for i, v := range validators {
        abciValidators[i] = abci.ValidatorUpdate{
            PubKey: abci.PubKey{
                Type: "ed25519",
                Data: v.PubKey,
            },
            Power: v.VotingPower,
        }
    }

    resp, err := a.client.InitChain(ctx, &abci.RequestInitChain{
        ChainId:         chainID,
        Validators:      abciValidators,
        Time:            time.Now(),
        AppStateBytes:   appState,
        InitialHeight:   1,
    })
    if err != nil {
        return err
    }

    a.mu.Lock()
    a.lastAppHash = resp.AppHash
    a.mu.Unlock()

    return nil
}

// PrepareProposal - 블록 제안 준비 (리더만 호출)
// ABCI 앱에게 트랜잭션 정렬/필터링 요청
func (a *ABCIAdapter) PrepareProposal(ctx context.Context, height int64, proposer []byte, txs [][]byte) ([][]byte, error) {
    req := &abci.RequestPrepareProposal{
        Txs:             txs,
        MaxTxBytes:      a.maxTxBytes,
        Height:          height,
        Time:            time.Now(),
        ProposerAddress: proposer,
        LocalLastCommit: abci.ExtendedCommitInfo{},
        Misbehavior:     []abci.Misbehavior{},
    }

    resp, err := a.client.PrepareProposal(ctx, req)
    if err != nil {
        return nil, fmt.Errorf("PrepareProposal failed: %w", err)
    }

    return resp.Txs, nil
}

// ProcessProposal - 블록 제안 검증 (PrePrepare 수신 시)
// 리턴: true=수락(ACCEPT), false=거부(REJECT)
func (a *ABCIAdapter) ProcessProposal(ctx context.Context, height int64, proposer []byte, txs [][]byte, hash []byte) (bool, error) {
    req := &abci.RequestProcessProposal{
        Txs:                txs,
        Hash:               hash,
        Height:             height,
        Time:               time.Now(),
        ProposerAddress:    proposer,
        ProposedLastCommit: abci.CommitInfo{},
        Misbehavior:        []abci.Misbehavior{},
    }

    resp, err := a.client.ProcessProposal(ctx, req)
    if err != nil {
        return false, fmt.Errorf("ProcessProposal failed: %w", err)
    }

    return resp.Status == abci.ResponseProcessProposal_ACCEPT, nil
}

// FinalizeBlock - 블록 실행 (Commit 단계 후)
// BeginBlock + DeliverTx(모든 tx) + EndBlock 통합
func (a *ABCIAdapter) FinalizeBlock(ctx context.Context, block *Block) (*ExecutionResult, error) {
    txs := make([][]byte, len(block.Transactions))
    for i, tx := range block.Transactions {
        txs[i] = tx.Data
    }

    req := &abci.RequestFinalizeBlock{
        Txs:               txs,
        Hash:              block.Hash,
        Height:            int64(block.Header.Height),
        Time:              block.Header.Timestamp,
        ProposerAddress:   []byte(block.Header.Proposer),
        DecidedLastCommit: abci.CommitInfo{},
        Misbehavior:       []abci.Misbehavior{},
    }

    resp, err := a.client.FinalizeBlock(ctx, req)
    if err != nil {
        return nil, fmt.Errorf("FinalizeBlock failed: %w", err)
    }

    // 결과 변환
    txResults := make([]TxResult, len(resp.TxResults))
    for i, r := range resp.TxResults {
        txResults[i] = TxResult{
            Code:      r.Code,
            Data:      r.Data,
            Log:       r.Log,
            GasWanted: r.GasWanted,
            GasUsed:   r.GasUsed,
        }
    }

    return &ExecutionResult{
        TxResults:             txResults,
        ValidatorUpdates:      resp.ValidatorUpdates,
        ConsensusParamUpdates: resp.ConsensusParamUpdates,
        AppHash:               resp.AppHash,
        Events:                resp.Events,
    }, nil
}

// Commit - 상태 커밋
func (a *ABCIAdapter) Commit(ctx context.Context) ([]byte, int64, error) {
    resp, err := a.client.Commit(ctx)
    if err != nil {
        return nil, 0, fmt.Errorf("Commit failed: %w", err)
    }

    a.mu.Lock()
    a.lastHeight++
    // RetainHeight는 v0.38에서 제거됨, resp에서 직접 데이터 사용하지 않음
    a.mu.Unlock()

    return nil, resp.RetainHeight, nil
}

// CheckTx - 트랜잭션 유효성 검사 (멤풀 진입 전)
func (a *ABCIAdapter) CheckTx(ctx context.Context, tx []byte) error {
    resp, err := a.client.CheckTx(ctx, tx)
    if err != nil {
        return err
    }

    if resp.Code != 0 {
        return fmt.Errorf("CheckTx failed (code=%d): %s", resp.Code, resp.Log)
    }

    return nil
}

// Query - 상태 쿼리
func (a *ABCIAdapter) Query(ctx context.Context, path string, data []byte, height int64, prove bool) (*QueryResult, error) {
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
        return nil, fmt.Errorf("query failed (code=%d): %s", resp.Code, resp.Log)
    }

    return &QueryResult{
        Key:      resp.Key,
        Value:    resp.Value,
        Height:   resp.Height,
        ProofOps: resp.ProofOps,
    }, nil
}

// GetLastAppHash - 마지막 앱 해시 반환
func (a *ABCIAdapter) GetLastAppHash() []byte {
    a.mu.RLock()
    defer a.mu.RUnlock()
    return a.lastAppHash
}

// GetLastHeight - 마지막 높이 반환
func (a *ABCIAdapter) GetLastHeight() int64 {
    a.mu.RLock()
    defer a.mu.RUnlock()
    return a.lastHeight
}

// SetLastAppHash - 앱 해시 설정
func (a *ABCIAdapter) SetLastAppHash(hash []byte) {
    a.mu.Lock()
    defer a.mu.Unlock()
    a.lastAppHash = hash
}

// ExecutionResult - 블록 실행 결과
type ExecutionResult struct {
    TxResults             []TxResult
    ValidatorUpdates      []abci.ValidatorUpdate
    ConsensusParamUpdates *abci.ConsensusParams
    AppHash               []byte
    Events                []abci.Event
}

// TxResult - 트랜잭션 실행 결과
type TxResult struct {
    Code      uint32
    Data      []byte
    Log       string
    GasWanted int64
    GasUsed   int64
}

// QueryResult - 쿼리 결과
type QueryResult struct {
    Key      []byte
    Value    []byte
    Height   int64
    ProofOps *abci.ProofOps
}

// Validator - 검증자 (내부 타입)
type Validator struct {
    ID          string
    PubKey      []byte
    VotingPower int64
}
```

---

## 7. PBFT 엔진 수정

### 7.1 pbft/engine.go 주요 변경 사항

```go
package pbft

import (
    "context"
    "encoding/json"
    "fmt"
    "log"
    "sync"
    "time"

    abci "github.com/cometbft/cometbft/abci/types"
)

// Engine 구조체 수정
type Engine struct {
    mu sync.RWMutex

    config      *Config
    view        uint64
    sequenceNum uint64

    validatorSet *ValidatorSet
    stateLog     *StateLog

    transport Transport

    // 기존 Application 인터페이스 대신 ABCIAdapter 사용
    abciAdapter *ABCIAdapter

    metrics *Metrics

    msgChan     chan *Message
    requestChan chan *RequestMsg

    viewChangeTimer   *time.Timer
    viewChangeManager *ViewChangeManager

    checkpoints map[uint64][]byte

    ctx    context.Context
    cancel context.CancelFunc

    logger *log.Logger

    committedBlocks []*Block

    // 앱 해시 저장
    lastAppHash []byte
}

// NewEngine 수정
func NewEngine(
    config *Config,
    validatorSet *ValidatorSet,
    transport Transport,
    abciAddress string, // ABCI 앱 주소 (예: "localhost:26658")
    m *Metrics,
) (*Engine, error) {
    ctx, cancel := context.WithCancel(context.Background())

    // ABCI 어댑터 생성
    abciAdapter, err := NewABCIAdapter(abciAddress)
    if err != nil {
        cancel()
        return nil, fmt.Errorf("failed to create ABCI adapter: %w", err)
    }

    engine := &Engine{
        config:          config,
        view:            0,
        sequenceNum:     0,
        validatorSet:    validatorSet,
        stateLog:        NewStateLog(config.WindowSize),
        transport:       transport,
        abciAdapter:     abciAdapter,
        metrics:         m,
        msgChan:         make(chan *Message, 1000),
        requestChan:     make(chan *RequestMsg, 1000),
        checkpoints:     make(map[uint64][]byte),
        ctx:             ctx,
        cancel:          cancel,
        logger:          log.Default(),
        committedBlocks: make([]*Block, 0),
    }

    // ViewChangeManager 초기화
    engine.viewChangeManager = NewViewChangeManager(config.NodeID, validatorSet.QuorumSize())
    engine.viewChangeManager.SetBroadcastFunc(engine.broadcast)
    engine.viewChangeManager.SetOnViewChangeComplete(engine.onViewChangeComplete)

    if transport != nil {
        transport.SetMessageHandler(engine.handleIncomingMessage)
    }

    return engine, nil
}

// InitChain - 체인 초기화 (시작 시 호출)
func (e *Engine) InitChain(ctx context.Context, chainID string, appState []byte) error {
    validators := make([]*Validator, len(e.validatorSet.Validators))
    for i, v := range e.validatorSet.Validators {
        validators[i] = &Validator{
            ID:          v.ID,
            PubKey:      v.PubKey,
            VotingPower: v.VotingPower,
        }
    }
    
    return e.abciAdapter.InitChain(ctx, chainID, validators, appState)
}

// proposeBlock 수정 - ABCI PrepareProposal 사용
func (e *Engine) proposeBlock(req *RequestMsg) {
    e.mu.Lock()
    e.sequenceNum++
    seqNum := e.sequenceNum
    view := e.view
    e.mu.Unlock()

    if !e.stateLog.IsInWindow(seqNum) {
        e.logger.Printf("[PBFT] Sequence number %d out of window", seqNum)
        return
    }

    // 트랜잭션 수집
    var txs [][]byte
    if req != nil {
        txs = append(txs, req.Operation)
    }
    // 추가 pending 트랜잭션이 있으면 수집
    // txs = append(txs, e.collectPendingTxs()...)

    // ABCI PrepareProposal 호출 - 앱에게 트랜잭션 정렬/필터링 요청
    ctx, cancel := context.WithTimeout(e.ctx, 5*time.Second)
    defer cancel()

    proposer := []byte(e.config.NodeID)
    preparedTxs, err := e.abciAdapter.PrepareProposal(ctx, int64(seqNum), proposer, txs)
    if err != nil {
        e.logger.Printf("[PBFT] PrepareProposal failed: %v", err)
        return
    }

    // 블록 생성
    var prevHash []byte
    if len(e.committedBlocks) > 0 {
        prevHash = e.committedBlocks[len(e.committedBlocks)-1].Hash
    }

    // 트랜잭션 변환
    transactions := make([]Transaction, len(preparedTxs))
    for i, txBytes := range preparedTxs {
        transactions[i] = Transaction{
            ID:        fmt.Sprintf("tx-%d-%d", seqNum, i),
            Data:      txBytes,
            Timestamp: time.Now(),
        }
    }

    block := NewBlock(seqNum, prevHash, e.config.NodeID, view, transactions)

    // PrePrepare 메시지 생성 및 저장
    prePrepareMsg := NewPrePrepareMsg(view, seqNum, block, e.config.NodeID)

    state := e.stateLog.GetState(view, seqNum)
    state.SetPrePrepare(prePrepareMsg, block)

    // 브로드캐스트
    payload, _ := json.Marshal(prePrepareMsg)
    msg := NewMessage(PrePrepare, view, seqNum, block.Hash, e.config.NodeID)
    msg.Payload = payload

    e.broadcast(msg)

    e.logger.Printf("[PBFT] Primary broadcast PRE-PREPARE for seq %d with %d txs", seqNum, len(transactions))
}

// handlePrePrepare 수정 - ABCI ProcessProposal 사용
func (e *Engine) handlePrePrepare(msg *Message) {
    if msg.NodeID != e.getPrimaryID() {
        e.logger.Printf("[PBFT] Received PRE-PREPARE from non-primary %s", msg.NodeID)
        return
    }

    e.mu.RLock()
    currentView := e.view
    e.mu.RUnlock()

    if msg.View != currentView {
        return
    }

    if !e.stateLog.IsInWindow(msg.SequenceNum) {
        return
    }

    var prePrepareMsg PrePrepareMsg
    if err := json.Unmarshal(msg.Payload, &prePrepareMsg); err != nil {
        e.logger.Printf("[PBFT] Failed to decode PRE-PREPARE: %v", err)
        return
    }

    // ABCI ProcessProposal 호출 - 블록 검증
    ctx, cancel := context.WithTimeout(e.ctx, 5*time.Second)
    defer cancel()

    txs := make([][]byte, len(prePrepareMsg.Block.Transactions))
    for i, tx := range prePrepareMsg.Block.Transactions {
        txs[i] = tx.Data
    }

    proposer := []byte(prePrepareMsg.PrimaryID)
    accepted, err := e.abciAdapter.ProcessProposal(
        ctx,
        int64(msg.SequenceNum),
        proposer,
        txs,
        msg.Digest,
    )
    if err != nil {
        e.logger.Printf("[PBFT] ProcessProposal error: %v", err)
        return
    }

    if !accepted {
        e.logger.Printf("[PBFT] ProcessProposal REJECTED block at seq %d", msg.SequenceNum)
        return
    }

    // StateLog에 저장
    state := e.stateLog.GetState(msg.View, msg.SequenceNum)
    state.SetPrePrepare(&prePrepareMsg, prePrepareMsg.Block)

    // Prepare 메시지 브로드캐스트
    prepareMsg := NewPrepareMsg(msg.View, msg.SequenceNum, msg.Digest, e.config.NodeID)
    payload, _ := json.Marshal(prepareMsg)
    prepareNetMsg := NewMessage(Prepare, msg.View, msg.SequenceNum, msg.Digest, e.config.NodeID)
    prepareNetMsg.Payload = payload

    e.broadcast(prepareNetMsg)
    e.resetViewChangeTimer()

    e.logger.Printf("[PBFT] Node %s sent PREPARE for seq %d (ProcessProposal ACCEPTED)", e.config.NodeID, msg.SequenceNum)
}

// executeBlock 수정 - ABCI FinalizeBlock + Commit 사용
func (e *Engine) executeBlock(state *State) {
    if state.Executed || state.Block == nil {
        return
    }

    startTime := time.Now()
    ctx, cancel := context.WithTimeout(e.ctx, 30*time.Second)
    defer cancel()

    // ABCI FinalizeBlock 호출
    result, err := e.abciAdapter.FinalizeBlock(ctx, state.Block)
    if err != nil {
        e.logger.Printf("[PBFT] FinalizeBlock failed: %v", err)
        return
    }

    // 트랜잭션 결과 확인
    failedCount := 0
    for i, txResult := range result.TxResults {
        if txResult.Code != 0 {
            e.logger.Printf("[PBFT] Tx %d failed (code=%d): %s", i, txResult.Code, txResult.Log)
            failedCount++
        }
    }
    if failedCount > 0 {
        e.logger.Printf("[PBFT] %d/%d transactions failed in block %d", failedCount, len(result.TxResults), state.SequenceNum)
    }

    // ABCI Commit 호출
    _, retainHeight, err := e.abciAdapter.Commit(ctx)
    if err != nil {
        e.logger.Printf("[PBFT] Commit failed: %v", err)
        return
    }

    // 앱 해시 저장
    e.mu.Lock()
    e.lastAppHash = result.AppHash
    e.abciAdapter.SetLastAppHash(result.AppHash)
    e.mu.Unlock()

    // 실행 완료 표시
    state.MarkExecuted()

    // 확정된 블록 목록에 추가
    e.mu.Lock()
    e.committedBlocks = append(e.committedBlocks, state.Block)
    e.mu.Unlock()

    // 메트릭 기록
    if e.metrics != nil {
        e.metrics.EndConsensusRound(state.SequenceNum)
        e.metrics.SetBlockHeight(state.SequenceNum)
        e.metrics.RecordBlockExecutionTime(time.Since(startTime))
        e.metrics.AddTransactions(len(state.Block.Transactions))
    }

    e.logger.Printf("[PBFT] Executed block at height %d with %d txs, appHash=%x, retainHeight=%d",
        state.SequenceNum, len(state.Block.Transactions), result.AppHash, retainHeight)

    // 검증자 업데이트 처리
    if len(result.ValidatorUpdates) > 0 {
        e.handleValidatorUpdates(result.ValidatorUpdates)
    }

    // 체크포인트 생성
    if state.SequenceNum%e.config.CheckpointInterval == 0 {
        e.createCheckpoint(state.SequenceNum)
    }
}

// handleValidatorUpdates - 검증자 업데이트 처리
func (e *Engine) handleValidatorUpdates(updates []abci.ValidatorUpdate) {
    e.mu.Lock()
    defer e.mu.Unlock()

    for _, update := range updates {
        pubKeyStr := string(update.PubKey.Data)
        if update.Power == 0 {
            // 검증자 제거
            e.validatorSet.Remove(pubKeyStr)
            e.logger.Printf("[PBFT] Validator removed: %s", pubKeyStr[:16])
        } else {
            // 검증자 추가/업데이트
            e.validatorSet.Update(&ValidatorInfo{
                ID:          pubKeyStr,
                PubKey:      update.PubKey.Data,
                VotingPower: update.Power,
            })
            e.logger.Printf("[PBFT] Validator updated: %s, power=%d", pubKeyStr[:16], update.Power)
        }
    }

    // ViewChangeManager quorum 업데이트
    e.viewChangeManager.UpdateQuorumSize(e.validatorSet.QuorumSize())
}

// GetLastAppHash - 마지막 앱 해시 반환
func (e *Engine) GetLastAppHash() []byte {
    e.mu.RLock()
    defer e.mu.RUnlock()
    return e.lastAppHash
}

// Close - 엔진 종료
func (e *Engine) Close() error {
    e.cancel()
    if e.abciAdapter != nil {
        return e.abciAdapter.Close()
    }
    return nil
}
```

---

## 8. gRPC 네트워크 레이어

### 8.1 transport/grpc.go

```go
package transport

import (
    "context"
    "fmt"
    "io"
    "net"
    "sync"
    "time"

    "google.golang.org/grpc"
    "google.golang.org/grpc/credentials/insecure"

    pbftv1 "github.com/ahwlsqja/pbft-cosmos/api/pbft/v1"
    "github.com/ahwlsqja/pbft-cosmos/pbft"
)

// GRPCTransport - gRPC 기반 네트워크 전송
type GRPCTransport struct {
    mu sync.RWMutex

    nodeID   string
    address  string
    server   *grpc.Server
    listener net.Listener

    // 피어 연결
    peers map[string]*peerConn

    // 메시지 핸들러
    msgHandler func(*pbft.Message)

    pbftv1.UnimplementedPBFTServiceServer
}

type peerConn struct {
    id     string
    addr   string
    conn   *grpc.ClientConn
    client pbftv1.PBFTServiceClient
}

// NewGRPCTransport 생성
func NewGRPCTransport(nodeID, address string) (*GRPCTransport, error) {
    return &GRPCTransport{
        nodeID:  nodeID,
        address: address,
        peers:   make(map[string]*peerConn),
    }, nil
}

// Start 서버 시작
func (t *GRPCTransport) Start() error {
    listener, err := net.Listen("tcp", t.address)
    if err != nil {
        return fmt.Errorf("failed to listen on %s: %w", t.address, err)
    }
    t.listener = listener

    t.server = grpc.NewServer()
    pbftv1.RegisterPBFTServiceServer(t.server, t)

    go func() {
        if err := t.server.Serve(listener); err != nil {
            fmt.Printf("gRPC server error: %v\n", err)
        }
    }()

    return nil
}

// Stop 서버 종료
func (t *GRPCTransport) Stop() {
    t.mu.Lock()
    defer t.mu.Unlock()

    for _, peer := range t.peers {
        peer.conn.Close()
    }
    t.peers = make(map[string]*peerConn)

    if t.server != nil {
        t.server.GracefulStop()
    }
}

// AddPeer 피어 추가
func (t *GRPCTransport) AddPeer(nodeID, address string) error {
    conn, err := grpc.Dial(
        address,
        grpc.WithTransportCredentials(insecure.NewCredentials()),
        grpc.WithBlock(),
        grpc.WithTimeout(5*time.Second),
    )
    if err != nil {
        return fmt.Errorf("failed to connect to peer %s at %s: %w", nodeID, address, err)
    }

    client := pbftv1.NewPBFTServiceClient(conn)

    t.mu.Lock()
    t.peers[nodeID] = &peerConn{
        id:     nodeID,
        addr:   address,
        conn:   conn,
        client: client,
    }
    t.mu.Unlock()

    return nil
}

// RemovePeer 피어 제거
func (t *GRPCTransport) RemovePeer(nodeID string) {
    t.mu.Lock()
    defer t.mu.Unlock()

    if peer, exists := t.peers[nodeID]; exists {
        peer.conn.Close()
        delete(t.peers, nodeID)
    }
}

// Broadcast 모든 피어에게 메시지 전송
func (t *GRPCTransport) Broadcast(msg *pbft.Message) error {
    t.mu.RLock()
    peers := make([]*peerConn, 0, len(t.peers))
    for _, peer := range t.peers {
        peers = append(peers, peer)
    }
    t.mu.RUnlock()

    protoMsg := messageToProto(msg)

    var wg sync.WaitGroup
    for _, peer := range peers {
        wg.Add(1)
        go func(p *peerConn) {
            defer wg.Done()
            ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
            defer cancel()

            _, err := p.client.BroadcastMessage(ctx, &pbftv1.BroadcastMessageRequest{
                Message: protoMsg,
            })
            if err != nil {
                fmt.Printf("[Transport] broadcast to %s failed: %v\n", p.id, err)
            }
        }(peer)
    }
    wg.Wait()

    return nil
}

// Send 특정 피어에게 메시지 전송
func (t *GRPCTransport) Send(nodeID string, msg *pbft.Message) error {
    t.mu.RLock()
    peer, exists := t.peers[nodeID]
    t.mu.RUnlock()

    if !exists {
        return fmt.Errorf("peer %s not found", nodeID)
    }

    protoMsg := messageToProto(msg)

    ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
    defer cancel()

    _, err := peer.client.SendMessage(ctx, &pbftv1.SendMessageRequest{
        TargetNodeId: nodeID,
        Message:      protoMsg,
    })
    return err
}

// SetMessageHandler 메시지 핸들러 설정
func (t *GRPCTransport) SetMessageHandler(handler func(*pbft.Message)) {
    t.msgHandler = handler
}

// gRPC 서비스 구현

func (t *GRPCTransport) BroadcastMessage(ctx context.Context, req *pbftv1.BroadcastMessageRequest) (*pbftv1.BroadcastMessageResponse, error) {
    if t.msgHandler != nil {
        msg := protoToMessage(req.Message)
        t.msgHandler(msg)
    }
    return &pbftv1.BroadcastMessageResponse{Success: true}, nil
}

func (t *GRPCTransport) SendMessage(ctx context.Context, req *pbftv1.SendMessageRequest) (*pbftv1.SendMessageResponse, error) {
    if t.msgHandler != nil {
        msg := protoToMessage(req.Message)
        t.msgHandler(msg)
    }
    return &pbftv1.SendMessageResponse{Success: true}, nil
}

func (t *GRPCTransport) MessageStream(stream pbftv1.PBFTService_MessageStreamServer) error {
    for {
        msg, err := stream.Recv()
        if err == io.EOF {
            return nil
        }
        if err != nil {
            return err
        }

        if t.msgHandler != nil {
            t.msgHandler(protoToMessage(msg))
        }
    }
}

func (t *GRPCTransport) SyncState(ctx context.Context, req *pbftv1.SyncStateRequest) (*pbftv1.SyncStateResponse, error) {
    // TODO: 상태 동기화 구현
    return &pbftv1.SyncStateResponse{}, nil
}

func (t *GRPCTransport) GetCheckpoint(ctx context.Context, req *pbftv1.GetCheckpointRequest) (*pbftv1.GetCheckpointResponse, error) {
    // TODO: 체크포인트 반환 구현
    return &pbftv1.GetCheckpointResponse{}, nil
}

// 헬퍼 함수들

func messageToProto(msg *pbft.Message) *pbftv1.PBFTMessage {
    return &pbftv1.PBFTMessage{
        Type:        pbftv1.MessageType(msg.Type),
        View:        msg.View,
        SequenceNum: msg.SequenceNum,
        Digest:      msg.Digest,
        NodeId:      msg.NodeID,
        Signature:   msg.Signature,
        Payload:     msg.Payload,
    }
}

func protoToMessage(proto *pbftv1.PBFTMessage) *pbft.Message {
    return &pbft.Message{
        Type:        pbft.MessageType(proto.Type),
        View:        proto.View,
        SequenceNum: proto.SequenceNum,
        Digest:      proto.Digest,
        NodeID:      proto.NodeId,
        Signature:   proto.Signature,
        Payload:     proto.Payload,
    }
}
```

---

## 9. 노드 실행

### 9.1 node/node.go

```go
package node

import (
    "context"
    "fmt"
    "log"
    "strings"
    "time"

    "github.com/ahwlsqja/pbft-cosmos/pbft"
    "github.com/ahwlsqja/pbft-cosmos/transport"
)

// Node - PBFT 노드
type Node struct {
    config    *Config
    engine    *pbft.Engine
    transport *transport.GRPCTransport
    logger    *log.Logger
}

// Config - 노드 설정
type Config struct {
    NodeID       string
    ListenAddr   string   // PBFT P2P 주소
    ABCIAddr     string   // ABCI 앱 주소
    Peers        []string // 피어 주소 목록 (형식: "nodeID@address")
    Validators   []*pbft.ValidatorInfo
    ChainID      string

    // PBFT 설정
    RequestTimeout     time.Duration
    ViewChangeTimeout  time.Duration
    CheckpointInterval uint64
    WindowSize         uint64
}

// NewNode 노드 생성
func NewNode(config *Config) (*Node, error) {
    // Transport 생성
    trans, err := transport.NewGRPCTransport(config.NodeID, config.ListenAddr)
    if err != nil {
        return nil, fmt.Errorf("failed to create transport: %w", err)
    }

    // ValidatorSet 생성
    validatorSet := pbft.NewValidatorSet(config.Validators)

    // PBFT 설정
    pbftConfig := &pbft.Config{
        NodeID:             config.NodeID,
        RequestTimeout:     config.RequestTimeout,
        ViewChangeTimeout:  config.ViewChangeTimeout,
        CheckpointInterval: config.CheckpointInterval,
        WindowSize:         config.WindowSize,
    }

    // PBFT 엔진 생성
    engine, err := pbft.NewEngine(
        pbftConfig,
        validatorSet,
        trans,
        config.ABCIAddr,
        nil, // metrics (옵션)
    )
    if err != nil {
        return nil, fmt.Errorf("failed to create engine: %w", err)
    }

    return &Node{
        config:    config,
        engine:    engine,
        transport: trans,
        logger:    log.Default(),
    }, nil
}

// Start 노드 시작
func (n *Node) Start(ctx context.Context) error {
    n.logger.Printf("[Node] Starting PBFT node %s", n.config.NodeID)

    // Transport 시작
    if err := n.transport.Start(); err != nil {
        return fmt.Errorf("failed to start transport: %w", err)
    }
    n.logger.Printf("[Node] Transport started on %s", n.config.ListenAddr)

    // 피어 연결
    for _, peerStr := range n.config.Peers {
        parts := strings.SplitN(peerStr, "@", 2)
        if len(parts) != 2 {
            n.logger.Printf("[Node] Invalid peer format: %s (expected nodeID@address)", peerStr)
            continue
        }
        peerID, peerAddr := parts[0], parts[1]

        if err := n.transport.AddPeer(peerID, peerAddr); err != nil {
            n.logger.Printf("[Node] Failed to connect to peer %s: %v", peerID, err)
        } else {
            n.logger.Printf("[Node] Connected to peer %s at %s", peerID, peerAddr)
        }
    }

    // PBFT 엔진 시작
    if err := n.engine.Start(); err != nil {
        return fmt.Errorf("failed to start engine: %w", err)
    }

    // InitChain 호출
    if err := n.engine.InitChain(ctx, n.config.ChainID, nil); err != nil {
        return fmt.Errorf("failed to init chain: %w", err)
    }

    n.logger.Printf("[Node] PBFT node %s started successfully", n.config.NodeID)
    return nil
}

// Stop 노드 종료
func (n *Node) Stop() error {
    n.logger.Printf("[Node] Stopping PBFT node %s", n.config.NodeID)

    if err := n.engine.Close(); err != nil {
        return err
    }
    n.transport.Stop()

    return nil
}

// SubmitTx 트랜잭션 제출
func (n *Node) SubmitTx(tx []byte, clientID string) error {
    return n.engine.SubmitRequest(tx, clientID)
}

// GetHeight 현재 높이 반환
func (n *Node) GetHeight() uint64 {
    return n.engine.GetCurrentHeight()
}

// GetView 현재 뷰 반환
func (n *Node) GetView() uint64 {
    return n.engine.GetCurrentView()
}

// GetAppHash 앱 해시 반환
func (n *Node) GetAppHash() []byte {
    return n.engine.GetLastAppHash()
}
```

### 9.2 cmd/pbftd/main.go

```go
package main

import (
    "context"
    "fmt"
    "os"
    "os/signal"
    "syscall"
    "time"

    "github.com/spf13/cobra"
    "github.com/spf13/viper"

    "github.com/ahwlsqja/pbft-cosmos/node"
    "github.com/ahwlsqja/pbft-cosmos/pbft"
)

var rootCmd = &cobra.Command{
    Use:   "pbftd",
    Short: "PBFT consensus node for Cosmos SDK v0.53",
}

var startCmd = &cobra.Command{
    Use:   "start",
    Short: "Start the PBFT node",
    RunE:  runStart,
}

func init() {
    rootCmd.AddCommand(startCmd)

    startCmd.Flags().String("node-id", "", "Node ID")
    startCmd.Flags().String("listen", "0.0.0.0:26656", "P2P listen address")
    startCmd.Flags().String("abci", "localhost:26658", "ABCI app address")
    startCmd.Flags().StringSlice("peers", []string{}, "Peer addresses (format: nodeID@address)")
    startCmd.Flags().String("chain-id", "pbft-chain", "Chain ID")
    startCmd.Flags().String("config", "", "Config file path")

    viper.BindPFlags(startCmd.Flags())
}

func runStart(cmd *cobra.Command, args []string) error {
    // 설정 로드
    config := &node.Config{
        NodeID:             viper.GetString("node-id"),
        ListenAddr:         viper.GetString("listen"),
        ABCIAddr:           viper.GetString("abci"),
        Peers:              viper.GetStringSlice("peers"),
        ChainID:            viper.GetString("chain-id"),
        RequestTimeout:     5 * time.Second,
        ViewChangeTimeout:  10 * time.Second,
        CheckpointInterval: 100,
        WindowSize:         200,
    }

    if config.NodeID == "" {
        return fmt.Errorf("--node-id is required")
    }

    // 검증자 설정 (실제로는 genesis.json에서 로드해야 함)
    // TODO: genesis 파일에서 로드하도록 수정
    config.Validators = []*pbft.ValidatorInfo{
        {ID: "node0", PubKey: []byte("pubkey0"), VotingPower: 10},
        {ID: "node1", PubKey: []byte("pubkey1"), VotingPower: 10},
        {ID: "node2", PubKey: []byte("pubkey2"), VotingPower: 10},
        {ID: "node3", PubKey: []byte("pubkey3"), VotingPower: 10},
    }

    // 노드 생성
    n, err := node.NewNode(config)
    if err != nil {
        return fmt.Errorf("failed to create node: %w", err)
    }

    // 컨텍스트
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    // 노드 시작
    if err := n.Start(ctx); err != nil {
        return fmt.Errorf("failed to start node: %w", err)
    }

    fmt.Printf("PBFT node %s is running...\n", config.NodeID)
    fmt.Printf("  P2P address: %s\n", config.ListenAddr)
    fmt.Printf("  ABCI address: %s\n", config.ABCIAddr)
    fmt.Printf("  Chain ID: %s\n", config.ChainID)

    // 종료 시그널 대기
    sigCh := make(chan os.Signal, 1)
    signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
    <-sigCh

    fmt.Println("\nShutting down...")
    return n.Stop()
}

func main() {
    if err := rootCmd.Execute(); err != nil {
        fmt.Fprintln(os.Stderr, err)
        os.Exit(1)
    }
}
```

---

## 10. Makefile

```makefile
.PHONY: proto build test clean deps all

# Go 설정
GOBIN := $(shell go env GOPATH)/bin

# Protobuf 생성
proto:
	@echo "Generating protobuf..."
	protoc --go_out=. --go-grpc_out=. \
		-I proto \
		proto/pbft/v1/*.proto

# 빌드
build:
	@echo "Building pbftd..."
	go build -o ./build/pbftd ./cmd/pbftd

# 테스트
test:
	go test -v ./...

# 통합 테스트
test-integration:
	./scripts/test-network.sh

# 정리
clean:
	rm -rf ./build
	rm -rf ./api

# 의존성
deps:
	go mod download
	go mod tidy

# 린트
lint:
	golangci-lint run ./...

# 전체 빌드
all: deps proto build test

# 개발용 실행 (4노드)
run-dev:
	@echo "Starting 4-node test network..."
	./scripts/run-dev.sh
```

---

## 11. 실행 순서

```bash
# 1. 의존성 설치
make deps

# 2. Protobuf 생성
make proto

# 3. 빌드
make build

# 4. 테스트
make test

# 5. Cosmos SDK 앱 실행 (별도 터미널에서)
# 예: simd start --grpc.address 0.0.0.0:9090

# 6. PBFT 노드 실행
./build/pbftd start \
    --node-id node0 \
    --listen 0.0.0.0:26656 \
    --abci localhost:26658 \
    --chain-id my-pbft-chain \
    --peers "node1@localhost:26657,node2@localhost:26658,node3@localhost:26659"
```

---

## 12. 핵심 변경 요약

```
기존 코드                         →  새 코드 (Cosmos SDK v0.53.0)
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

Application 인터페이스            →  ABCIAdapter + abciclient
  (자체 정의)                         (CometBFT v0.38 ABCI 타입)

ValidateBlock()                  →  ProcessProposal()
  (자체 검증)                         (RequestProcessProposal)

ExecuteBlock()                   →  FinalizeBlock()
  (BeginBlock+DeliverTx+EndBlock)    (RequestFinalizeBlock - 통합)

GetPendingTransactions()         →  PrepareProposal()
  (자체 구현)                         (RequestPrepareProposal)

Commit()                         →  Commit()
  (자체 구현)                         (RequestCommit)

JSON 메시지                       →  Protobuf 메시지
TCP 직접 통신                     →  gRPC 통신

Import 경로:
- github.com/cometbft/cometbft/abci/types (ABCI 타입)
- github.com/cosmos/cosmos-sdk (SDK 타입)
```

---

## 13. 구현해야 할 파일 목록

| 번호 | 파일 | 설명 | 난이도 |
|-----|------|------|--------|
| 1 | `proto/pbft/v1/types.proto` | Protobuf 메시지 정의 | ★☆☆ |
| 2 | `proto/pbft/v1/service.proto` | gRPC 서비스 정의 | ★☆☆ |
| 3 | `abci/types.go` | ABCI 타입 변환 함수 | ★★☆ |
| 4 | `abci/client.go` | ABCI 클라이언트 | ★★☆ |
| 5 | `pbft/abci_adapter.go` | **NEW** PBFT-ABCI 어댑터 | ★★★ |
| 6 | `pbft/engine.go` | 기존 엔진 수정 | ★★★★ |
| 7 | `transport/grpc.go` | **NEW** gRPC 네트워크 | ★★★ |
| 8 | `node/node.go` | **NEW** 노드 구조체 | ★★☆ |
| 9 | `cmd/pbftd/main.go` | **NEW** 실행 파일 | ★★☆ |
| 10 | 테스트 및 Makefile | 빌드/테스트 | ★★☆ |

---

## 14. 작업 예상 시간

```
1. Protobuf 정의 및 생성:        1-2시간
2. ABCI 타입/클라이언트:         2-3시간
3. ABCI 어댑터:                 4-5시간
4. engine.go 수정:              5-7시간
5. gRPC 네트워크:               3-4시간
6. 노드/실행 코드:              2-3시간
7. 테스트 및 디버깅:            5-8시간

총: 약 25-35시간 (풀타임 4-5일)
```

---

## 15. 주의사항

1. **CometBFT v0.38.x ABCI 타입 사용**
   - `RequestFinalizeBlock` / `ResponseFinalizeBlock`
   - `RequestPrepareProposal` / `ResponsePrepareProposal`
   - `RequestProcessProposal` / `ResponseProcessProposal`
   - BeginBlock, DeliverTx, EndBlock은 더 이상 개별 메서드 아님

2. **Cosmos SDK v0.53.0 호환성**
   - `cosmossdk.io/core` 모듈 사용
   - `cosmossdk.io/log` 로거 사용
   - `cosmossdk.io/store` 스토어 타입

3. **gRPC 통신**
   - PBFT 노드 간 P2P: 자체 gRPC 서비스
   - PBFT → ABCI 앱: CometBFT ABCI gRPC 클라이언트

4. **검증자 업데이트**
   - `ResponseFinalizeBlock.ValidatorUpdates` 처리
   - ValidatorSet 동적 변경 지원