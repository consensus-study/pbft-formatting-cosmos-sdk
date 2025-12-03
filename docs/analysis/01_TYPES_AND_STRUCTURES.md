# PBFT-Cosmos 타입 및 구조체 분석

## 1. 핵심 타입 정의

### 1.1 Consensus 패키지 (consensus/pbft/)

#### Engine (consensus/pbft/engine.go) - PBFT 합의 엔진
```go
// Engine - PBFT 합의 알고리즘의 메인 엔진
// 블록 제안, 투표, 커밋, 뷰 변경 등 모든 합의 로직을 처리
type Engine struct {
    mu sync.RWMutex  // 동시성 제어용 뮤텍스

    // 기본 설정
    config *Config  // PBFT 설정 (타임아웃, 윈도우 크기 등)

    // PBFT 상태
    view        uint64              // 현재 뷰 번호 (리더 결정에 사용: view % N)
    sequenceNum uint64              // 현재 시퀀스 번호 (= 블록 높이)

    // 검증자 관리
    validatorSet *types.ValidatorSet  // 검증자 목록 및 현재 제안자

    // 상태 관리
    stateLog *StateLog  // PBFT 상태 로그 (Pre-Prepare, Prepare, Commit 메시지 저장)

    // 외부 의존성
    transport Transport          // 네트워크 통신 계층
    app       Application        // ABCI 애플리케이션 인터페이스
    mempool   *mempool.Mempool   // 트랜잭션 풀
    metrics   *metrics.Metrics   // Prometheus 메트릭

    // 채널 (비동기 처리)
    msgChan     chan *Message     // 수신 메시지 처리 채널
    requestChan chan *RequestMsg  // 클라이언트 요청 처리 채널

    // 뷰 변경 관리
    viewChangeTimer   *time.Timer        // 뷰 변경 타임아웃 타이머
    viewChangeManager *ViewChangeManager // 뷰 변경 로직 관리자

    // 체크포인트
    checkpoints map[uint64][]byte  // 시퀀스 번호 → 상태 다이제스트

    // 종료 관리
    ctx    context.Context     // 종료 컨텍스트
    cancel context.CancelFunc  // 종료 함수

    // 로깅
    logger *log.Logger  // 로거

    // 커밋된 블록 저장
    committedBlocks []*types.Block  // 확정된 블록 목록
}
```

#### Config (consensus/pbft/engine.go) - PBFT 설정
```go
// Config - PBFT 엔진 설정 구조체
type Config struct {
    NodeID string  // 노드 고유 식별자

    RequestTimeout    time.Duration  // 요청 타임아웃 (기본: 5초)
    ViewChangeTimeout time.Duration  // 뷰 변경 타임아웃 (기본: 10초)

    CheckpointInterval uint64  // 체크포인트 생성 주기 (기본: 100블록마다)
    WindowSize         uint64  // 윈도우 크기 - 동시 처리 가능한 시퀀스 범위 (기본: 200)
}
```

#### Transport / Application 인터페이스 (consensus/pbft/engine.go)
```go
// Transport - 네트워크 통신 인터페이스
// GRPCTransport가 이 인터페이스를 구현
type Transport interface {
    Broadcast(msg *Message) error           // 모든 노드에게 메시지 브로드캐스트
    Send(nodeID string, msg *Message) error // 특정 노드에 메시지 전송
    SetMessageHandler(handler func(*Message)) // 수신 메시지 핸들러 설정
}

// Application - ABCI 애플리케이션 인터페이스
// ABCIAdapter가 이 인터페이스를 구현
type Application interface {
    ExecuteBlock(block *types.Block) ([]byte, error)  // 블록 실행 후 상태 해시 반환
    ValidateBlock(block *types.Block) error           // 블록 유효성 검증
    GetPendingTransactions() []types.Transaction      // 대기 중인 트랜잭션 조회
    Commit(block *types.Block) error                  // 블록 최종 커밋
}
```

#### MessageType (api/pbft/v1/types.pb.go)
```go
// MessageType - PBFT 프로토콜 메시지 타입
type MessageType int32

const (
    MessageType_MESSAGE_TYPE_UNSPECIFIED MessageType = 0  // 미지정 (에러)
    MessageType_MESSAGE_TYPE_PRE_PREPARE MessageType = 1  // Pre-Prepare: 리더가 블록 제안
    MessageType_MESSAGE_TYPE_PREPARE     MessageType = 2  // Prepare: 검증자가 제안 승인
    MessageType_MESSAGE_TYPE_COMMIT      MessageType = 3  // Commit: 검증자가 커밋 준비 완료
    MessageType_MESSAGE_TYPE_VIEW_CHANGE MessageType = 4  // View-Change: 리더 교체 요청
    MessageType_MESSAGE_TYPE_NEW_VIEW    MessageType = 5  // New-View: 새 리더가 뷰 시작
    MessageType_MESSAGE_TYPE_CHECKPOINT  MessageType = 6  // Checkpoint: 상태 체크포인트
)
```

#### PBFTMessage (api/pbft/v1/types.pb.go)
```go
// PBFTMessage - PBFT 프로토콜의 기본 메시지 구조
// 모든 PBFT 메시지가 이 포맷으로 전송됨
type PBFTMessage struct {
    Type        MessageType            // 메시지 타입 (PRE_PREPARE, PREPARE, COMMIT 등)
    View        uint64                 // 뷰 번호 (리더 식별에 사용)
    SequenceNum uint64                 // 시퀀스 번호 (= 블록 높이)
    Digest      []byte                 // 블록/데이터 해시 (무결성 검증)
    NodeId      string                 // 메시지 발신 노드 ID
    Timestamp   *timestamppb.Timestamp // 메시지 생성 시간
    Signature   []byte                 // 발신자 서명
    Payload     []byte                 // 실제 데이터 (JSON 직렬화된 PrePrepareMsg, PrepareMsg 등)
}
```

#### PrePrepareMsg (consensus/pbft/messages.go)
```go
// PrePrepareMsg - Pre-Prepare 단계 메시지
// 리더(Primary)가 새 블록을 제안할 때 전송
// 흐름: 리더 → 모든 검증자
type PrePrepareMsg struct {
    View        uint64       `json:"view"`         // 뷰 번호 (어떤 리더가 보냈는지 확인)
    SequenceNum uint64       `json:"sequence_num"` // 시퀀스 번호 (= 블록 높이)
    Digest      []byte       `json:"digest"`       // 블록 해시 (SHA-256)
    Block       *types.Block `json:"block"`        // 제안 블록 전체 데이터
    PrimaryID   string       `json:"primary_id"`   // 리더 노드 ID
}
```

#### PrepareMsg / CommitMsg (consensus/pbft/messages.go)
```go
// PrepareMsg - Prepare 단계 메시지
// 검증자가 Pre-Prepare를 수신하고 블록을 검증한 후 전송
// 흐름: 각 검증자 → 모든 노드 (브로드캐스트)
// 조건: 2f+1개 수집 시 Prepared 상태 달성
type PrepareMsg struct {
    View        uint64 `json:"view"`         // 뷰 번호
    SequenceNum uint64 `json:"sequence_num"` // 시퀀스 번호
    Digest      []byte `json:"digest"`       // 블록 해시 (Pre-Prepare와 동일해야 함)
    NodeId      string `json:"node_id"`      // Prepare를 보낸 노드 ID
}

// CommitMsg - Commit 단계 메시지
// Prepared 상태가 된 후 각 검증자가 전송
// 흐름: 각 검증자 → 모든 노드 (브로드캐스트)
// 조건: 2f+1개 수집 시 Committed 상태 → 블록 확정
type CommitMsg struct {
    View        uint64 `json:"view"`         // 뷰 번호
    SequenceNum uint64 `json:"sequence_num"` // 시퀀스 번호
    Digest      []byte `json:"digest"`       // 블록 해시
    NodeId      string `json:"node_id"`      // Commit을 보낸 노드 ID
}
```

#### ViewChangeMsg / NewViewMsg (consensus/pbft/view_change.go)
```go
// ViewChangeMsg - 뷰 변경 요청 메시지
// 리더가 응답하지 않거나 비잔틴 행동 시 검증자가 전송
// 흐름: 불만 있는 검증자 → 새 리더 후보
type ViewChangeMsg struct {
    NewView     uint64          `json:"new_view"`      // 요청하는 새 뷰 번호 (현재 뷰 + 1)
    LastSeqNum  uint64          `json:"last_seq_num"`  // 마지막 안정된 시퀀스 (체크포인트)
    Checkpoints []*Checkpoint   `json:"checkpoints"`   // 체크포인트 증명 (상태 검증용)
    PreparedSet []*PreparedCert `json:"prepared_set"`  // 준비됐지만 커밋 안된 블록들
    NodeId      string          `json:"node_id"`       // 뷰 변경 요청한 노드 ID
}

// NewViewMsg - 새 뷰 시작 메시지
// 새 리더가 2f+1개 ViewChange 수집 후 전송
// 흐름: 새 리더 → 모든 노드
type NewViewMsg struct {
    View           uint64            `json:"view"`             // 새 뷰 번호
    ViewChangeMsgs []*ViewChangeMsg  `json:"view_change_msgs"` // 수집한 ViewChange 메시지들 (증명)
    PrePrepareMsgs []*PrePrepareMsg  `json:"pre_prepare_msgs"` // 새 뷰에서 재제안할 블록들
    NewPrimaryId   string            `json:"new_primary_id"`   // 새 리더 노드 ID
}
```

#### Block / BlockHeader (types/types.go)
```go
// Block - 블록 구조체
// PBFT에서 합의를 거쳐 확정되는 데이터 단위
type Block struct {
    Header       BlockHeader   `json:"header"`       // 블록 메타데이터
    Transactions []Transaction `json:"transactions"` // 포함된 트랜잭션 목록
    Hash         []byte        `json:"hash"`         // 블록 해시 (Header + Transactions)
}

// BlockHeader - 블록 헤더
// 블록의 메타데이터를 담는 구조체
type BlockHeader struct {
    Height      uint64    `json:"height"`       // 블록 높이 (체인 길이)
    PrevHash    []byte    `json:"prev_hash"`    // 이전 블록 해시 (체인 연결)
    Timestamp   time.Time `json:"timestamp"`    // 블록 생성 시간
    ProposerID  string    `json:"proposer_id"`  // 블록 제안자 (리더) ID
    StateRoot   []byte    `json:"state_root"`   // 실행 후 상태 머클 루트 (ABCI AppHash)
    TxRoot      []byte    `json:"tx_root"`      // 트랜잭션 머클 루트
    View        uint64    `json:"view"`         // 이 블록이 제안된 뷰 번호
    SequenceNum uint64    `json:"sequence_num"` // PBFT 시퀀스 번호 (= Height)
}
```

#### Checkpoint / PreparedCert (consensus/pbft/messages.go)
```go
// Checkpoint - 체크포인트 메시지
// 주기적으로 상태 스냅샷을 저장하여 로그 정리 및 빠른 복구에 사용
// 기본적으로 CheckpointInterval(100) 블록마다 생성
type Checkpoint struct {
    SequenceNum uint64 `json:"sequence_num"` // 체크포인트 시퀀스 번호
    Digest      []byte `json:"digest"`       // 해당 시점의 상태 해시 (AppHash)
    NodeId      string `json:"node_id"`      // 체크포인트 보낸 노드 ID
}

// PreparedCert - Prepared 인증서
// ViewChange 시 "이 블록이 Prepared 됐음"을 증명하는 데이터
type PreparedCert struct {
    PrePrepare *PrePrepareMsg `json:"pre_prepare"` // 원본 Pre-Prepare 메시지
    Prepares   []*PrepareMsg  `json:"prepares"`    // 2f+1개의 Prepare 메시지들
}
```

#### Transaction / Validator (types/types.go)
```go
// Transaction - 트랜잭션 구조체
// 클라이언트가 제출하는 요청 데이터
type Transaction struct {
    ID        string    `json:"id"`        // 트랜잭션 고유 ID (해시 기반)
    Data      []byte    `json:"data"`      // 트랜잭션 페이로드 (앱이 해석)
    Timestamp time.Time `json:"timestamp"` // 트랜잭션 생성 시간
    Signature []byte    `json:"signature"` // 발신자 서명
    From      string    `json:"from"`      // 발신자 주소/ID
}

// Validator - 검증자 정보
// PBFT 합의에 참여하는 노드 정보
type Validator struct {
    ID        string `json:"id"`         // 검증자 고유 ID
    Address   string `json:"address"`    // 네트워크 주소
    PublicKey []byte `json:"public_key"` // 공개키 (서명 검증용)
    Power     int64  `json:"power"`      // 투표력 (가중치, 모든 노드 동일 시 1)
}

// ValidatorSet - 검증자 집합
// 현재 활성 검증자들의 목록
type ValidatorSet struct {
    Validators []*Validator `json:"validators"` // 검증자 목록
    Proposer   *Validator   `json:"proposer"`   // 현재 뷰의 제안자 (리더)
}
```

---

### 1.2 ABCI 어댑터 (consensus/pbft/abci_adapter.go)

#### ABCIAdapter
```go
// ABCIAdapter - ABCI 2.0 애플리케이션 어댑터
// PBFT Engine과 Cosmos SDK ABCI 앱 사이의 브릿지 역할
// CometBFT v0.38.x의 ABCI 2.0 인터페이스 구현
type ABCIAdapter struct {
    mu sync.RWMutex  // 동시성 제어

    client *abciclient.Client  // ABCI gRPC 클라이언트 (앱과 통신)

    // 상태 추적
    lastHeight  int64   // 마지막으로 커밋된 블록 높이
    lastAppHash []byte  // 마지막 앱 상태 해시 (다음 블록 헤더에 포함)

    // 설정
    maxTxBytes int64   // 단일 트랜잭션 최대 크기 (기본: 1MB)
    chainID    string  // 체인 ID (블록 서명에 사용)
}
```

**ABCI 2.0 메서드 흐름:**
```
PrepareProposal → ProcessProposal → FinalizeBlock → Commit
```
- **PrepareProposal**: 리더가 블록 제안 전 트랜잭션 정렬/필터링
- **ProcessProposal**: 검증자가 제안된 블록 유효성 검사
- **FinalizeBlock**: 블록 실행 (트랜잭션 처리, 상태 변경)
- **Commit**: 상태 영구 저장

#### ABCIExecutionResult
```go
// ABCIExecutionResult - FinalizeBlock 실행 결과
// 블록 실행 후 앱에서 반환하는 데이터
type ABCIExecutionResult struct {
    TxResults        []ABCITxResult          // 각 트랜잭션 실행 결과
    ValidatorUpdates []abci.ValidatorUpdate  // 검증자 변경 (추가/삭제/power 변경)
    AppHash          []byte                  // 새 앱 상태 해시 (머클 루트)
    Events           []abci.Event            // 발생한 이벤트 (인덱싱용)
}
```

#### ABCITxResult
```go
// ABCITxResult - 개별 트랜잭션 실행 결과
type ABCITxResult struct {
    Code      uint32  // 결과 코드 (0=성공, 그 외=에러 코드)
    Data      []byte  // 실행 결과 데이터 (앱 정의)
    Log       string  // 로그 메시지 (디버깅용)
    Info      string  // 추가 정보
    GasWanted int64   // 요청한 가스량
    GasUsed   int64   // 실제 사용한 가스량
}
```

#### ABCIQueryResult / ABCIInfo
```go
// ABCIQueryResult - Query 요청 결과
// 앱 상태를 읽기 전용으로 조회할 때 사용
type ABCIQueryResult struct {
    Key    []byte  // 쿼리한 키
    Value  []byte  // 조회된 값
    Height int64   // 쿼리 시점의 블록 높이
}

// ABCIInfo - 앱 정보
// Info 요청으로 앱의 현재 상태 조회
type ABCIInfo struct {
    Version          string  // 앱 버전 문자열
    AppVersion       uint64  // 앱 프로토콜 버전
    LastBlockHeight  int64   // 앱이 처리한 마지막 블록 높이
    LastBlockAppHash []byte  // 마지막 블록의 앱 상태 해시
}
```

---

### 1.3 ABCI 클라이언트 (abci/)

#### Client (abci/client.go)
```go
// Client - ABCI gRPC 클라이언트
// Cosmos SDK 앱과 gRPC로 통신하는 클라이언트
type Client struct {
    mu sync.RWMutex  // 동시성 제어

    // gRPC 연결
    conn   *grpc.ClientConn  // gRPC 연결 객체
    client abci.ABCIClient   // ABCI 서비스 클라이언트

    // 앱 상태 캐시 (성능 최적화)
    lastHeight  int64   // 마지막으로 확인한 블록 높이
    lastAppHash []byte  // 마지막 앱 상태 해시

    // 설정
    address string         // ABCI 앱 주소 (예: "localhost:26658")
    timeout time.Duration  // 요청 타임아웃
}

// ClientConfig - 클라이언트 설정
type ClientConfig struct {
    Address string         // ABCI 앱 gRPC 주소 (예: "localhost:26658")
    Timeout time.Duration  // 요청 타임아웃 (기본: 10초)
}
```

#### BlockData / ExecutionResult (abci/types.go)
```go
// BlockData - FinalizeBlock에 전달할 블록 데이터
// PBFT Block을 ABCI 형식으로 변환한 구조체
type BlockData struct {
    Height       int64      // 블록 높이
    Txs          [][]byte   // 트랜잭션 목록 (원시 바이트)
    Hash         []byte     // 블록 해시
    Time         time.Time  // 블록 생성 시간
    ProposerAddr []byte     // 블록 제안자 주소 (검증자 주소)
}

// ExecutionResult - FinalizeBlock 실행 결과
// 앱에서 반환한 블록 실행 결과
type ExecutionResult struct {
    TxResults             []TxResult                // 각 트랜잭션 실행 결과
    ValidatorUpdates      []abci.ValidatorUpdate    // 검증자 변경사항
    ConsensusParamUpdates *cmttypes.ConsensusParams // 합의 파라미터 변경 (블록 크기 등)
    AppHash               []byte                    // 새 앱 상태 해시
    Events                []abci.Event              // 블록 레벨 이벤트
}

// TxResult - 개별 트랜잭션 실행 결과
type TxResult struct {
    Code      uint32        // 결과 코드 (0=성공)
    Data      []byte        // 실행 결과 데이터
    Log       string        // 로그 메시지
    Info      string        // 추가 정보
    GasWanted int64         // 요청 가스
    GasUsed   int64         // 사용 가스
    Events    []abci.Event  // 트랜잭션 레벨 이벤트
}

// QueryResult - Query 요청 결과
type QueryResult struct {
    Key      []byte            // 쿼리 키
    Value    []byte            // 결과 값
    Height   int64             // 쿼리 높이
    ProofOps *crypto.ProofOps  // 머클 증명 (라이트 클라이언트용)
}

// ValidatorUpdate - 검증자 업데이트 정보
type ValidatorUpdate struct {
    PubKey []byte  // 공개키 (Ed25519, 32바이트)
    Power  int64   // 투표력 (0이면 삭제)
}
```

---

### 1.4 Mempool 패키지 (mempool/)

#### Tx (mempool/tx.go)
```go
// Tx - Mempool에 저장되는 트랜잭션 래퍼
// 원본 트랜잭션 데이터와 메타데이터를 함께 관리
type Tx struct {
    // 식별자
    Hash []byte  // SHA-256 해시 (중복 체크에 사용)
    ID   string  // 해시의 hex 문자열 (맵 키로 사용)

    // 데이터
    Data []byte  // 원본 트랜잭션 바이트 (ABCI 앱으로 전달)

    // 메타데이터 (트랜잭션 정렬/필터링용)
    Sender    string     // 발신자 주소 (sender별 nonce 관리)
    Nonce     uint64     // 발신자별 순차 번호 (리플레이 방지)
    GasPrice  uint64     // 가스 가격 (우선순위 정렬 - 현재 미사용, FIFO)
    GasLimit  uint64     // 가스 한도
    Timestamp time.Time  // Mempool 진입 시간 (FIFO 정렬 기준)

    // 상태
    Height    int64      // CheckTx 시점의 블록 높이
    CheckedAt time.Time  // 마지막 검증 시간
}
```

#### Mempool (mempool/mempool.go)
```go
// Mempool - 트랜잭션 풀
// 블록에 포함되기 전 대기 중인 트랜잭션들을 관리
// FIFO 순서로 트랜잭션 반환 (ABCI 2.0 설계: 정렬은 PrepareProposal에서)
type Mempool struct {
    mu sync.RWMutex  // 동시성 제어

    config *Config  // Mempool 설정

    // 트랜잭션 저장소
    txStore     map[string]*Tx    // txID(hash) → Tx 매핑
    senderIndex map[string][]*Tx  // sender → []*Tx (sender별 트랜잭션 목록)

    // 현재 상태
    txCount   int    // 현재 저장된 트랜잭션 수
    txBytes   int64  // 현재 총 바이트 크기
    height    int64  // 현재 블록 높이
    isRunning bool   // 실행 상태

    // 발신자별 Nonce 추적
    senderNonce map[string]uint64  // sender → 마지막 nonce (리플레이 방지)

    // 최근 제거된 트랜잭션 캐시 (재진입 방지)
    recentlyRemoved map[string]time.Time  // txID → 제거 시간

    // 콜백
    checkTxCallback CheckTxCallback  // ABCI CheckTx 콜백 (외부에서 주입)

    // 이벤트 채널
    newTxCh chan *Tx  // 새 트랜잭션 알림 (Reactor에서 브로드캐스트용)

    // 종료 관리
    ctx    context.Context
    cancel context.CancelFunc

    // 메트릭
    metrics *MempoolMetrics  // Prometheus 메트릭
}

// Config - Mempool 설정
type Config struct {
    // 크기 제한
    MaxTxs      int    // 최대 트랜잭션 수 (기본: 5000)
    MaxBytes    int64  // 최대 총 바이트 (기본: 1GB)
    MaxTxBytes  int    // 단일 트랜잭션 최대 바이트 (기본: 1MB)
    MaxBatchTxs int    // ReapMaxTxs에서 한 번에 가져올 최대 수 (기본: 500)

    // TTL (Time To Live)
    TTL time.Duration  // 트랜잭션 만료 시간 (기본: 10분, 오래된 tx 제거)

    // 재검사 (블록 커밋 후 남은 트랜잭션 재검증)
    RecheckEnabled bool           // 재검사 활성화 여부
    RecheckTimeout time.Duration  // 재검사 타임아웃

    // 캐시
    CacheSize int  // recentlyRemoved 캐시 크기 (기본: 10000)

    // 최소 가스 가격 (낮은 가격 tx 거부)
    MinGasPrice uint64
}

// CheckTxCallback - 트랜잭션 검증 콜백
// ABCI CheckTx를 호출하여 트랜잭션 유효성 검사
type CheckTxCallback func(tx *Tx) error
```

#### MempoolMetrics
```go
// MempoolMetrics - Mempool 메트릭 (Prometheus 연동 가능)
type MempoolMetrics struct {
    mu sync.RWMutex  // 동시성 제어

    // 트랜잭션 카운터
    TxsReceived  int64  // 총 수신 트랜잭션 수
    TxsAccepted  int64  // 수락된 트랜잭션 수 (검증 통과)
    TxsRejected  int64  // 거부된 트랜잭션 수 (검증 실패)
    TxsExpired   int64  // TTL 만료로 제거된 트랜잭션 수
    TxsEvicted   int64  // 용량 초과로 퇴출된 트랜잭션 수
    TxsCommitted int64  // 블록에 포함되어 제거된 트랜잭션 수

    // 재검사
    RecheckCount int64  // 재검사 실행 횟수

    // 현재 상태
    CurrentSize  int    // 현재 트랜잭션 수
    CurrentBytes int64  // 현재 총 바이트

    // 피크 (최대치 기록)
    PeakSize  int    // 최대 트랜잭션 수 (워터마크)
    PeakBytes int64  // 최대 바이트 (워터마크)

    // 타이밍
    LastBlockTime time.Time  // 마지막 블록 커밋 시간
}
```

**ABCI 2.0 설계 철학:**
- **Mempool**: FIFO 순서로 트랜잭션 저장/반환 (단순성, O(1) 삽입/삭제)
- **PrepareProposal**: ABCI 앱에서 트랜잭션 정렬/필터링 담당 (유연성)
- `ReapMaxTxs()`는 도착 순서(Timestamp)로 반환하고, 정렬은 앱의 `PrepareProposal`에서 처리

#### Reactor (mempool/reactor.go)
```go
// Reactor - Mempool 네트워크 리액터
// 새 트랜잭션을 다른 노드에 브로드캐스트하는 역할
type Reactor struct {
    mu sync.RWMutex  // 동시성 제어

    config  *ReactorConfig  // 리액터 설정
    mempool *Mempool        // 연결된 Mempool

    broadcaster    Broadcaster  // 네트워크 브로드캐스터 인터페이스
    broadcastQueue chan *Tx     // 브로드캐스트 대기 큐

    isRunning bool              // 실행 상태
    ctx       context.Context   // 종료 컨텍스트
    cancel    context.CancelFunc
}

// ReactorConfig - 리액터 설정
type ReactorConfig struct {
    BroadcastEnabled  bool           // 브로드캐스트 활성화 여부
    BroadcastDelay    time.Duration  // 배치 지연 (기본: 10ms, 여러 tx 모아서 전송)
    MaxBroadcastBatch int            // 한 번에 브로드캐스트할 최대 tx 수 (기본: 100)
    MaxPendingTxs     int            // 대기 큐 최대 크기 (기본: 10000)
}

// Broadcaster - 네트워크 브로드캐스트 인터페이스
// GRPCTransport를 래핑한 transportBroadcaster가 구현
type Broadcaster interface {
    BroadcastTx(tx []byte) error              // 모든 피어에게 트랜잭션 전송
    SendTx(peerID string, tx []byte) error    // 특정 피어에게 트랜잭션 전송
}
```

---

### 1.5 Network/Transport 패키지 (transport/)

#### StateProvider (transport/grpc.go)
```go
// StateProvider - 상태 동기화를 위한 데이터 제공 인터페이스
// Engine이 이 인터페이스를 구현하여 GRPCTransport에 주입
// SyncState, GetCheckpoint RPC 처리에 사용
type StateProvider interface {
    // GetBlocksFromHeight - 지정된 높이부터 블록들을 반환 (동기화용)
    GetBlocksFromHeight(fromHeight uint64) []*types.Block
    // GetCheckpoints - 저장된 모든 체크포인트들을 반환
    GetCheckpoints() []pbft.Checkpoint
    // GetCheckpoint - 특정 시퀀스 번호의 체크포인트 반환
    GetCheckpoint(seqNum uint64) (*pbft.Checkpoint, bool)
}
```

#### GRPCTransport (transport/grpc.go)
```go
// GRPCTransport - gRPC 기반 P2P 통신 계층
// PBFT 메시지를 노드 간에 전송하는 네트워크 레이어
// PBFTServiceServer 인터페이스를 구현
type GRPCTransport struct {
    mu sync.RWMutex  // 동시성 제어

    // 기본 정보
    nodeID  string  // 이 노드의 ID
    address string  // 리슨 주소 (예: "0.0.0.0:26656")

    // gRPC 서버
    server   *grpc.Server   // gRPC 서버 인스턴스
    listener net.Listener   // TCP 리스너

    // 피어 연결 관리
    peers map[string]*peerConn  // nodeID → peerConn 매핑

    // 메시지 핸들러 (Engine에서 설정)
    msgHandler func(*pbft.Message)  // 수신 메시지 처리 콜백

    // 상태 동기화 데이터 제공자
    stateProvider StateProvider  // SyncState/GetCheckpoint 처리용

    // 상태 관리
    running bool           // 실행 상태
    done    chan struct{}  // 종료 신호 채널

    // gRPC 서비스 임베딩 (전방 호환성)
    pbftv1.UnimplementedPBFTServiceServer
}

// peerConn - 개별 피어 연결 정보
type peerConn struct {
    id     string                    // 피어 노드 ID
    addr   string                    // 피어 주소 (예: "192.168.1.2:26656")
    conn   *grpc.ClientConn          // gRPC 클라이언트 연결
    client pbftv1.PBFTServiceClient  // gRPC 클라이언트 (메시지 전송용)
}

// GRPCTransportConfig - Transport 설정
type GRPCTransportConfig struct {
    NodeID  string  // 노드 ID
    Address string  // 리슨 주소
}
```

#### TransportInterface
```go
// TransportInterface - PBFT Transport 계층 인터페이스
// 다양한 구현 가능 (gRPC, TCP, mock 등)
// Engine의 Transport 의존성으로 주입됨
type TransportInterface interface {
    Start() error                              // 서버 시작
    Stop()                                     // 서버 종료
    AddPeer(nodeID, address string) error      // 피어 연결 추가
    RemovePeer(nodeID string)                  // 피어 연결 제거
    Broadcast(msg *pbft.Message) error         // 모든 피어에게 메시지 브로드캐스트
    Send(nodeID string, msg *pbft.Message) error // 특정 피어에게 메시지 전송
    SetMessageHandler(handler func(*pbft.Message)) // 수신 메시지 핸들러 설정
    GetPeers() []string                        // 연결된 피어 ID 목록
    PeerCount() int                            // 연결된 피어 수
}
```

---

### 1.6 Crypto 패키지 (crypto/)

#### KeyPair
```go
// KeyPair - 공개키/개인키 쌍
// ECDSA P-256 (secp256r1) 알고리즘 사용
type KeyPair struct {
    PrivateKey *ecdsa.PrivateKey  // ECDSA 개인키 (서명 생성용)
    PublicKey  *ecdsa.PublicKey   // ECDSA 공개키 (서명 검증용)
}
```

#### Signature
```go
// Signature - ECDSA 서명 구조체
// R, S 값을 별도로 저장 (DER 인코딩 대신)
type Signature struct {
    R []byte  // ECDSA R 값 (32바이트)
    S []byte  // ECDSA S 값 (32바이트)
}
```

#### Signer / DefaultSigner
```go
// Signer - 서명자 인터페이스
// 메시지 서명 및 검증 기능 제공
type Signer interface {
    Sign(data []byte) (*Signature, error)     // 데이터에 서명 생성
    Verify(data []byte, sig *Signature) bool  // 서명 검증
    PublicKeyBytes() []byte                   // 공개키 바이트 반환
}

// DefaultSigner - 기본 서명자 구현
// ECDSA P-256 알고리즘으로 서명
type DefaultSigner struct {
    keyPair *KeyPair  // 키 쌍
}
```

---

### 1.7 Metrics 패키지 (metrics/)

#### Metrics (metrics/prometheus.go)
```go
// Metrics - Prometheus 메트릭 수집기
// PBFT 합의 과정의 각종 메트릭을 수집하여 모니터링
// /metrics 엔드포인트로 Prometheus가 스크래핑
type Metrics struct {
    mu sync.RWMutex  // 동시성 제어

    // 합의 메트릭
    consensusRoundsTotal prometheus.Counter    // 총 합의 라운드 수 (완료된 블록 수)
    consensusDuration    prometheus.Histogram  // 합의 소요 시간 (Pre-Prepare → Commit)
    currentBlockHeight   prometheus.Gauge      // 현재 블록 높이
    currentView          prometheus.Gauge      // 현재 뷰 번호 (리더 변경 추적)

    // 메시지 메트릭 (타입별)
    messagesSentTotal     *prometheus.CounterVec   // 타입별 전송 메시지 수 (label: type)
    messagesReceivedTotal *prometheus.CounterVec   // 타입별 수신 메시지 수 (label: type)
    messageProcessingTime *prometheus.HistogramVec // 메시지 처리 시간 (label: type)

    // 뷰 변경 메트릭
    viewChangesTotal prometheus.Counter  // 뷰 변경 총 횟수 (리더 장애 지표)

    // 블록 메트릭
    blockExecutionTime prometheus.Histogram  // 블록 실행 시간 (ABCI FinalizeBlock)
    transactionsTotal  prometheus.Counter    // 총 처리 트랜잭션 수
    tps                prometheus.Gauge      // 초당 트랜잭션 (TPS)

    // 내부 추적용 (Prometheus에 노출 안됨)
    roundStartTimes map[uint64]time.Time  // 시퀀스별 라운드 시작 시간
    txCount         int64                 // TPS 계산용 트랜잭션 카운터
    lastTpsUpdate   time.Time             // 마지막 TPS 업데이트 시간
}

// Server - 메트릭 HTTP 서버
type Server struct {
    addr   string        // 리슨 주소 (예: "0.0.0.0:26660")
    server *http.Server  // HTTP 서버 인스턴스
}
```

**메트릭 엔드포인트:**
- `/metrics` - Prometheus 스크래핑 엔드포인트
- `/health` - 헬스 체크 (OK 반환)
- `/status` - 노드 상태 JSON (node_id, chain_id, view, height 등)

---

### 1.8 Node 패키지 (node/)

#### Node (node/node.go)
```go
// Node - PBFT 합의 노드
// 모든 컴포넌트(Engine, Transport, Mempool, Metrics)를 통합하여 관리
// CLI에서 `pbftd start`로 실행됨
type Node struct {
    mu sync.RWMutex  // 동시성 제어

    // 핵심 컴포넌트
    config    *Config                   // 노드 설정
    engine    *pbft.Engine              // PBFT 합의 엔진
    transport *transport.GRPCTransport  // P2P 네트워크 통신
    mempool   *mempool.Mempool          // 트랜잭션 풀
    reactor   *mempool.Reactor          // Mempool 네트워크 리액터 (tx 브로드캐스트)
    metrics   *metrics.Metrics          // Prometheus 메트릭

    // 상태
    running bool           // 노드 실행 상태
    done    chan struct{}  // 종료 신호 채널

    // 로깅
    logger *log.Logger  // 로거

    // 메트릭 서버
    metricsServer *http.Server  // Prometheus 메트릭 HTTP 서버
}
```

#### Config (node/config.go)
```go
// Config - 노드 설정
// CLI 플래그 또는 설정 파일에서 로드
type Config struct {
    // 노드 식별
    NodeID  string  // 노드 고유 ID (예: "node0")
    ChainID string  // 체인 ID (예: "pbft-chain")

    // 네트워크 주소
    ListenAddr string  // P2P 리슨 주소 (예: "0.0.0.0:26656")
    ABCIAddr   string  // ABCI 앱 주소 (예: "localhost:26658")

    // 피어 및 검증자
    Peers      []string            // 피어 목록 (형식: "nodeID@address")
    Validators []*types.Validator  // 검증자 목록 (BFT: 최소 4개)

    // PBFT 타이밍 설정
    RequestTimeout     time.Duration  // 요청 타임아웃 (기본: 5초)
    ViewChangeTimeout  time.Duration  // 뷰 변경 타임아웃 (기본: 10초)
    CheckpointInterval uint64         // 체크포인트 주기 (기본: 100블록)
    WindowSize         uint64         // PBFT 윈도우 크기 (기본: 200)

    // 메트릭
    MetricsEnabled bool    // 메트릭 활성화 여부
    MetricsAddr    string  // 메트릭 서버 주소 (예: "0.0.0.0:26660")

    // 기타
    LogLevel string  // 로그 레벨 (debug, info, warn, error)
    DataDir  string  // 데이터 디렉토리 경로
}
```

**노드 시작 순서:**
1. `NewNode()` - Transport, Mempool, Reactor, Engine 생성
2. `Start()` - Transport 시작 → 피어 연결 → Mempool 시작 → Reactor 시작 → Metrics 서버 시작 → Engine 시작
3. 종료 시 역순으로 `Stop()`

---

### 1.9 gRPC 서비스 (api/pbft/v1/service_grpc.pb.go)

#### PBFTServiceServer (서버 인터페이스)
```go
// PBFTServiceServer - gRPC 서버가 구현해야 하는 인터페이스
type PBFTServiceServer interface {
    // BroadcastMessage - 모든 피어에게 PBFT 메시지 브로드캐스트
    BroadcastMessage(context.Context, *BroadcastMessageRequest) (*BroadcastMessageResponse, error)
    // SendMessage - 특정 노드에 PBFT 메시지 전송
    SendMessage(context.Context, *SendMessageRequest) (*SendMessageResponse, error)
    // MessageStream - 양방향 메시지 스트림 (실시간 P2P 통신)
    MessageStream(PBFTService_MessageStreamServer) error
    // SyncState - 노드 간 상태 동기화 (블록, 체크포인트)
    SyncState(context.Context, *SyncStateRequest) (*SyncStateResponse, error)
    // GetCheckpoint - 특정 시퀀스의 체크포인트 조회
    GetCheckpoint(context.Context, *GetCheckpointRequest) (*GetCheckpointResponse, error)
    // GetStatus - 노드 상태 조회 (뷰, 높이, 피어 수 등)
    GetStatus(context.Context, *GetStatusRequest) (*GetStatusResponse, error)
    mustEmbedUnimplementedPBFTServiceServer()  // 전방 호환성
}
```

#### PBFTServiceClient (클라이언트 인터페이스)
```go
// PBFTServiceClient - gRPC 클라이언트가 사용하는 인터페이스
type PBFTServiceClient interface {
    // BroadcastMessage - 메시지 브로드캐스트 요청
    BroadcastMessage(ctx context.Context, in *BroadcastMessageRequest, opts ...grpc.CallOption) (*BroadcastMessageResponse, error)
    // SendMessage - 특정 노드에 메시지 전송 요청
    SendMessage(ctx context.Context, in *SendMessageRequest, opts ...grpc.CallOption) (*SendMessageResponse, error)
    // MessageStream - 양방향 스트림 생성
    MessageStream(ctx context.Context, opts ...grpc.CallOption) (PBFTService_MessageStreamClient, error)
    // SyncState - 상태 동기화 요청
    SyncState(ctx context.Context, in *SyncStateRequest, opts ...grpc.CallOption) (*SyncStateResponse, error)
    // GetCheckpoint - 체크포인트 조회 요청
    GetCheckpoint(ctx context.Context, in *GetCheckpointRequest, opts ...grpc.CallOption) (*GetCheckpointResponse, error)
    // GetStatus - 노드 상태 조회 요청
    GetStatus(ctx context.Context, in *GetStatusRequest, opts ...grpc.CallOption) (*GetStatusResponse, error)
}
```

#### Request/Response 타입
```go
// BroadcastMessageRequest - 브로드캐스트 요청
type BroadcastMessageRequest struct {
    Message *PBFTMessage `json:"message,omitempty"`  // 브로드캐스트할 PBFT 메시지
}

// BroadcastMessageResponse - 브로드캐스트 응답
type BroadcastMessageResponse struct {
    Success bool   `json:"success,omitempty"`  // 성공 여부
    Error   string `json:"error,omitempty"`    // 에러 메시지 (실패 시)
}

// SendMessageRequest - 특정 노드 전송 요청
type SendMessageRequest struct {
    TargetNodeId string       `json:"target_node_id,omitempty"`  // 대상 노드 ID
    Message      *PBFTMessage `json:"message,omitempty"`         // 전송할 PBFT 메시지
}

// SendMessageResponse - 특정 노드 전송 응답
type SendMessageResponse struct {
    Success bool   `json:"success,omitempty"`  // 성공 여부
    Error   string `json:"error,omitempty"`    // 에러 메시지 (실패 시)
}

// SyncStateRequest - 상태 동기화 요청
type SyncStateRequest struct {
    FromHeight uint64 `json:"from_height,omitempty"`  // 시작 높이
    ToHeight   uint64 `json:"to_height,omitempty"`    // 종료 높이
}

// SyncStateResponse - 상태 동기화 응답
type SyncStateResponse struct {
    Blocks      []*Block      `json:"blocks,omitempty"`       // 요청 범위의 블록들
    Checkpoints []*Checkpoint `json:"checkpoints,omitempty"`  // 체크포인트들
}

// GetCheckpointRequest - 체크포인트 조회 요청
type GetCheckpointRequest struct {
    SequenceNum uint64 `json:"sequence_num,omitempty"`  // 조회할 시퀀스 번호
}

// GetCheckpointResponse - 체크포인트 조회 응답
type GetCheckpointResponse struct {
    Checkpoint *Checkpoint `json:"checkpoint,omitempty"`  // 체크포인트 정보
    StateHash  []byte      `json:"state_hash,omitempty"`  // 상태 해시
}

// GetStatusRequest - 상태 조회 요청 (빈 구조체)
type GetStatusRequest struct{}

// GetStatusResponse - 상태 조회 응답
type GetStatusResponse struct {
    NodeId        string `json:"node_id,omitempty"`        // 노드 ID
    CurrentView   uint64 `json:"current_view,omitempty"`   // 현재 뷰 번호
    CurrentHeight uint64 `json:"current_height,omitempty"` // 현재 블록 높이
    LastAppHash   []byte `json:"last_app_hash,omitempty"`  // 마지막 앱 상태 해시
    PeerCount     int32  `json:"peer_count,omitempty"`     // 연결된 피어 수
    IsPrimary     bool   `json:"is_primary,omitempty"`     // 현재 리더 여부
}
```

#### MessageStream 인터페이스
```go
// PBFTService_MessageStreamClient - 클라이언트측 양방향 스트림
type PBFTService_MessageStreamClient interface {
    Send(*PBFTMessage) error      // 메시지 전송
    Recv() (*PBFTMessage, error)  // 메시지 수신
    grpc.ClientStream             // 기본 클라이언트 스트림
}

// PBFTService_MessageStreamServer - 서버측 양방향 스트림
type PBFTService_MessageStreamServer interface {
    Send(*PBFTMessage) error      // 메시지 전송
    Recv() (*PBFTMessage, error)  // 메시지 수신
    grpc.ServerStream             // 기본 서버 스트림
}
```

#### 서비스 등록 및 디스크립터
```go
// UnimplementedPBFTServiceServer - 미구현 서버 (전방 호환성용)
// 새 메서드 추가 시 기존 구현 깨지지 않음
type UnimplementedPBFTServiceServer struct{}

// RegisterPBFTServiceServer - gRPC 서버에 서비스 등록
func RegisterPBFTServiceServer(s grpc.ServiceRegistrar, srv PBFTServiceServer)

// NewPBFTServiceClient - 새 클라이언트 생성
func NewPBFTServiceClient(cc grpc.ClientConnInterface) PBFTServiceClient

// PBFTService_ServiceDesc - 서비스 디스크립터
var PBFTService_ServiceDesc = grpc.ServiceDesc{
    ServiceName: "pbft.v1.PBFTService",  // 서비스 전체 이름
    HandlerType: (*PBFTServiceServer)(nil),
    Methods: []grpc.MethodDesc{
        {MethodName: "BroadcastMessage", Handler: ...},  // 단일 요청
        {MethodName: "SendMessage", Handler: ...},
        {MethodName: "SyncState", Handler: ...},
        {MethodName: "GetCheckpoint", Handler: ...},
        {MethodName: "GetStatus", Handler: ...},
    },
    Streams: []grpc.StreamDesc{
        {StreamName: "MessageStream", ..., ServerStreams: true, ClientStreams: true},  // 양방향
    },
}
```

---

## 2. 에러 타입

### Mempool 에러
```go
var (
    ErrTxAlreadyExists  = errors.New("transaction already exists")
    ErrMempoolFull      = errors.New("mempool is full")
    ErrTxTooLarge       = errors.New("transaction too large")
    ErrTxExpired        = errors.New("transaction expired")
    ErrInvalidTx        = errors.New("invalid transaction")
    ErrMempoolNotRunning = errors.New("mempool is not running")
)
```

### Config 에러
```go
const (
    ErrEmptyNodeID            = configError("node ID is required")
    ErrEmptyChainID           = configError("chain ID is required")
    ErrEmptyListenAddr        = configError("listen address is required")
    ErrInsufficientValidators = configError("at least 4 validators are required for BFT")
)
```
