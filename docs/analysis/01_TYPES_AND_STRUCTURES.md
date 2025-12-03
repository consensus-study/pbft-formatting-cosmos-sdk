# PBFT-Cosmos 타입 및 구조체 분석

## 1. 핵심 타입 정의

### 1.1 Consensus 패키지 (consensus/pbft/)

#### MessageType (api/pbft/v1/types.pb.go)
```go
type MessageType int32

const (
    MessageType_MESSAGE_TYPE_UNSPECIFIED MessageType = 0
    MessageType_MESSAGE_TYPE_PRE_PREPARE MessageType = 1  // 리더가 블록 제안
    MessageType_MESSAGE_TYPE_PREPARE     MessageType = 2  // 검증자가 제안 승인
    MessageType_MESSAGE_TYPE_COMMIT      MessageType = 3  // 검증자가 커밋 준비
    MessageType_MESSAGE_TYPE_VIEW_CHANGE MessageType = 4  // 뷰 변경 요청
    MessageType_MESSAGE_TYPE_NEW_VIEW    MessageType = 5  // 새 뷰 시작
    MessageType_MESSAGE_TYPE_CHECKPOINT  MessageType = 6  // 체크포인트
)
```

#### PBFTMessage (api/pbft/v1/types.pb.go)
```go
type PBFTMessage struct {
    Type        MessageType            // 메시지 타입
    View        uint64                 // 현재 뷰 번호
    SequenceNum uint64                 // 시퀀스 번호 (블록 높이)
    Digest      []byte                 // 블록/데이터 해시
    NodeId      string                 // 발신 노드 ID
    Timestamp   *timestamppb.Timestamp // 타임스탬프
    Signature   []byte                 // 서명
    Payload     []byte                 // 실제 데이터 (PrePrepare, Prepare 등)
}
```

#### PrePrepareMsg (consensus/pbft/messages.go)
```go
type PrePrepareMsg struct {
    View        uint64       `json:"view"`         // 뷰 번호
    SequenceNum uint64       `json:"sequence_num"` // 시퀀스 번호 (블록 높이)
    Digest      []byte       `json:"digest"`       // 블록 해시
    Block       *types.Block `json:"block"`        // 블록 객체 포인터
    PrimaryID   string       `json:"primary_id"`   // 리더 노드 ID
}
```

#### PrepareMsg / CommitMsg
```go
type PrepareMsg struct {
    View        uint64  // 뷰 번호
    SequenceNum uint64  // 시퀀스 번호
    Digest      []byte  // 블록 해시
    NodeId      string  // 노드 ID
}

type CommitMsg struct {
    View        uint64  // 뷰 번호
    SequenceNum uint64  // 시퀀스 번호
    Digest      []byte  // 다이제스트
    NodeId      string  // 노드 ID
}
```

#### ViewChangeMsg / NewViewMsg
```go
type ViewChangeMsg struct {
    NewView     uint64          // 요청하는 새 뷰 번호
    LastSeqNum  uint64          // 마지막 안정 시퀀스
    Checkpoints []*Checkpoint   // 체크포인트 증명
    PreparedSet []*PreparedCert // 준비된 인증서들
    NodeId      string          // 노드 ID
}

type NewViewMsg struct {
    View           uint64            // 새 뷰 번호
    ViewChangeMsgs []*ViewChangeMsg  // 뷰 변경 메시지들
    PrePrepareMsgs []*PrePrepareMsg  // 새 뷰의 PrePrepare들
    NewPrimaryId   string            // 새 리더 노드 ID
}
```

#### Block / BlockHeader (types/types.go)
```go
type Block struct {
    Header       BlockHeader   `json:"header"`       // 블록 헤더 (값 타입)
    Transactions []Transaction `json:"transactions"` // 트랜잭션 목록
    Hash         []byte        `json:"hash"`         // 블록 해시
}

type BlockHeader struct {
    Height      uint64    `json:"height"`       // 블록 높이
    PrevHash    []byte    `json:"prev_hash"`    // 이전 블록 해시
    Timestamp   time.Time `json:"timestamp"`    // 타임스탬프 (Go time.Time)
    ProposerID  string    `json:"proposer_id"`  // 제안자 ID
    StateRoot   []byte    `json:"state_root"`   // 상태 해시
    TxRoot      []byte    `json:"tx_root"`      // 트랜잭션 머클 루트
    View        uint64    `json:"view"`         // 뷰 번호
    SequenceNum uint64    `json:"sequence_num"` // 시퀀스 번호
}
```

#### Checkpoint / PreparedCert
```go
type Checkpoint struct {
    SequenceNum uint64  // 시퀀스 번호
    Digest      []byte  // 상태 다이제스트
    NodeId      string  // 노드 ID
}

type PreparedCert struct {
    PrePrepare *PrePrepareMsg  // PrePrepare 메시지
    Prepares   []*PrepareMsg   // Prepare 메시지들
}
```

#### Transaction / Validator (types/types.go)
```go
type Transaction struct {
    ID        string    `json:"id"`        // 트랜잭션 ID
    Data      []byte    `json:"data"`      // 트랜잭션 데이터
    Timestamp time.Time `json:"timestamp"` // 타임스탬프 (Go time.Time)
    Signature []byte    `json:"signature"` // 서명
    From      string    `json:"from"`      // 발신자
}

type Validator struct {
    ID        string `json:"id"`         // 검증자 ID
    Address   string `json:"address"`    // 주소
    PublicKey []byte `json:"public_key"` // 공개키
    Power     int64  `json:"power"`      // 투표력
}

type ValidatorSet struct {
    Validators []*Validator `json:"validators"` // 검증자 목록
    Proposer   *Validator   `json:"proposer"`   // 현재 제안자
}
```

---

### 1.2 ABCI 어댑터 (consensus/pbft/abci_adapter.go)

#### ABCIAdapter
```go
type ABCIAdapter struct {
    mu          sync.RWMutex
    client      *abciclient.Client  // ABCI gRPC 클라이언트
    lastHeight  int64               // 마지막 커밋된 높이
    lastAppHash []byte              // 마지막 앱 해시
    maxTxBytes  int64               // 최대 트랜잭션 크기 (기본 1MB)
    chainID     string              // 체인 ID
}
```

#### ABCIExecutionResult
```go
type ABCIExecutionResult struct {
    TxResults        []ABCITxResult           // 각 트랜잭션 실행 결과
    ValidatorUpdates []abci.ValidatorUpdate   // 검증자 변경사항
    AppHash          []byte                   // 새 앱 상태 해시
    Events           []abci.Event             // 발생한 이벤트들
}
```

#### ABCITxResult
```go
type ABCITxResult struct {
    Code      uint32  // 결과 코드 (0=성공)
    Data      []byte  // 반환 데이터
    Log       string  // 로그 메시지
    Info      string  // 추가 정보
    GasWanted int64   // 요청 가스
    GasUsed   int64   // 사용 가스
}
```

#### ABCIQueryResult / ABCIInfo
```go
type ABCIQueryResult struct {
    Key    []byte  // 쿼리 키
    Value  []byte  // 결과 값
    Height int64   // 쿼리 높이
}

type ABCIInfo struct {
    Version          string  // 앱 버전
    AppVersion       uint64  // 앱 버전 번호
    LastBlockHeight  int64   // 마지막 블록 높이
    LastBlockAppHash []byte  // 마지막 앱 해시
}
```

---

### 1.3 ABCI 클라이언트 (abci/)

#### Client (abci/client.go)
```go
type Client struct {
    mu     sync.RWMutex
    conn   *grpc.ClientConn  // gRPC 연결
    client abci.ABCIClient   // ABCI 클라이언트

    // 앱 정보 캐시
    lastHeight  int64   // 마지막 높이
    lastAppHash []byte  // 마지막 앱 해시

    // 설정
    address string         // 연결 주소
    timeout time.Duration  // 타임아웃
}

type ClientConfig struct {
    Address string         // "localhost:26658"
    Timeout time.Duration  // 요청 타임아웃 (기본 10초)
}
```

#### BlockData / ExecutionResult (abci/types.go)
```go
type BlockData struct {
    Height       int64      // 블록 높이
    Txs          [][]byte   // 트랜잭션들
    Hash         []byte     // 블록 해시
    Time         time.Time  // 타임스탬프
    ProposerAddr []byte     // 제안자 주소
}

type ExecutionResult struct {
    TxResults             []TxResult                // 트랜잭션 결과들
    ValidatorUpdates      []abci.ValidatorUpdate    // 검증자 업데이트
    ConsensusParamUpdates *cmttypes.ConsensusParams // 합의 파라미터 (CometBFT types)
    AppHash               []byte                    // 앱 해시
    Events                []abci.Event              // 이벤트들
}

type TxResult struct {
    Code      uint32
    Data      []byte
    Log       string
    Info      string
    GasWanted int64
    GasUsed   int64
    Events    []abci.Event  // 트랜잭션별 이벤트
}

type QueryResult struct {
    Key      []byte
    Value    []byte
    Height   int64
    ProofOps *crypto.ProofOps  // 증명 정보 (tendermint/crypto)
}

type ValidatorUpdate struct {
    PubKey []byte  // 공개키 (Ed25519)
    Power  int64   // 투표력
}
```

---

### 1.4 Mempool 패키지 (mempool/)

#### Tx (mempool/tx.go)
```go
type Tx struct {
    // 식별자
    Hash []byte  // SHA256 해시
    ID   string  // hex 문자열

    // 데이터
    Data []byte  // 원본 트랜잭션 바이트

    // 메타데이터
    Sender    string     // 발신자 주소
    Nonce     uint64     // 발신자별 순차 번호
    GasPrice  uint64     // 가스 가격 (우선순위)
    GasLimit  uint64     // 가스 한도
    Timestamp time.Time  // 멤풀 진입 시간

    // 상태
    Height    int64      // CheckTx 시점 블록 높이
    CheckedAt time.Time  // 검증 시간
}
```

#### Mempool (mempool/mempool.go)
```go
type Mempool struct {
    mu sync.RWMutex

    config *Config  // 설정

    // 트랜잭션 저장소
    txStore     map[string]*Tx      // txHash → Tx
    senderIndex map[string][]*Tx    // sender → []*Tx (nonce 정렬)

    // 현재 상태
    txCount   int    // 현재 트랜잭션 수
    txBytes   int64  // 현재 총 바이트
    height    int64  // 현재 블록 높이
    isRunning bool

    // 발신자별 마지막 nonce 추적
    senderNonce map[string]uint64  // sender → lastNonce

    // 최근 제거된 트랜잭션 캐시 (중복 방지)
    recentlyRemoved map[string]time.Time

    // 콜백
    checkTxCallback CheckTxCallback  // CheckTx 콜백

    // 브로드캐스트 채널
    newTxCh chan *Tx  // 새 트랜잭션 알림

    // 종료
    ctx    context.Context
    cancel context.CancelFunc

    // 메트릭
    metrics *MempoolMetrics
}

type Config struct {
    // 크기 제한
    MaxTxs      int    // 최대 트랜잭션 수 (기본: 5000)
    MaxBytes    int64  // 최대 바이트 (기본: 1GB)
    MaxTxBytes  int    // 단일 트랜잭션 최대 바이트 (기본: 1MB)
    MaxBatchTxs int    // 한 번에 가져올 최대 트랜잭션 수 (기본: 500)

    // TTL (Time To Live)
    TTL time.Duration  // 트랜잭션 만료 시간 (기본: 10분)

    // 재검사
    RecheckEnabled bool           // 블록 후 재검사 활성화
    RecheckTimeout time.Duration  // 재검사 타임아웃

    // 캐시
    CacheSize int  // 최근 제거된 tx 캐시 크기

    // 최소 가스 가격
    MinGasPrice uint64
}

// CheckTxCallback - 트랜잭션 검증 콜백 함수
type CheckTxCallback func(tx *Tx) error
```

#### MempoolMetrics
```go
type MempoolMetrics struct {
    mu sync.RWMutex

    TxsReceived   int64      // 받은 총 트랜잭션 수
    TxsAccepted   int64      // 수락된 트랜잭션 수
    TxsRejected   int64      // 거부된 트랜잭션 수
    TxsExpired    int64      // 만료된 트랜잭션 수
    TxsEvicted    int64      // 퇴출된 트랜잭션 수
    TxsCommitted  int64      // 커밋된 트랜잭션 수
    RecheckCount  int64      // 재검사 횟수
    CurrentSize   int        // 현재 크기
    CurrentBytes  int64      // 현재 바이트
    PeakSize      int        // 최대 크기
    PeakBytes     int64      // 최대 바이트
    LastBlockTime time.Time  // 마지막 블록 시간
}
```

**ABCI 2.0 설계 철학:**
- Mempool: FIFO 순서로 트랜잭션 저장/반환 (단순성, O(1) 삽입/삭제)
- PrepareProposal: ABCI 앱에서 트랜잭션 정렬/필터링 담당 (유연성)
- `ReapMaxTxs()`는 도착 순서(Timestamp)로 반환하고, 정렬은 앱의 `PrepareProposal`에서 처리

#### Reactor (mempool/reactor.go)
```go
type Reactor struct {
    mu sync.RWMutex

    config  *ReactorConfig
    mempool *Mempool

    broadcaster    Broadcaster       // 네트워크 브로드캐스터
    broadcastQueue chan *Tx          // 브로드캐스트 큐

    isRunning bool
    ctx       context.Context
    cancel    context.CancelFunc
}

type ReactorConfig struct {
    BroadcastEnabled  bool           // 브로드캐스트 활성화
    BroadcastDelay    time.Duration  // 배치 지연 (10ms)
    MaxBroadcastBatch int            // 최대 배치 크기 (100)
    MaxPendingTxs     int            // 대기 최대 tx 수 (10000)
}

type Broadcaster interface {
    BroadcastTx(tx []byte) error       // 모든 피어에게 전송
    SendTx(peerID string, tx []byte) error  // 특정 피어에게 전송
}
```

---

### 1.5 Network/Transport 패키지

#### StateProvider (transport/grpc.go)
```go
// StateProvider 상태 동기화를 위한 데이터 제공 인터페이스
type StateProvider interface {
    // GetBlocksFromHeight 지정된 높이부터 블록들을 반환
    GetBlocksFromHeight(fromHeight uint64) []*types.Block
    // GetCheckpoints 저장된 체크포인트들을 반환
    GetCheckpoints() []pbft.Checkpoint
    // GetCheckpoint 특정 시퀀스 번호의 체크포인트 반환
    GetCheckpoint(seqNum uint64) (*pbft.Checkpoint, bool)
}
```

#### GRPCTransport (transport/grpc.go)
```go
type GRPCTransport struct {
    mu sync.RWMutex

    nodeID   string          // 노드 ID
    address  string          // 리슨 주소
    server   *grpc.Server    // gRPC 서버
    listener net.Listener    // TCP 리스너

    // Peer connections
    peers map[string]*peerConn  // nodeID → peerConn

    // Message handler callback
    msgHandler func(*pbft.Message)  // 메시지 핸들러

    // State provider for sync operations
    stateProvider StateProvider  // 상태 동기화용 데이터 제공자

    // Running state
    running bool              // 실행 상태
    done    chan struct{}     // 종료 채널

    // Embed the unimplemented server for forward compatibility
    pbftv1.UnimplementedPBFTServiceServer
}

type peerConn struct {
    id     string                      // 피어 노드 ID
    addr   string                      // 피어 주소
    conn   *grpc.ClientConn            // gRPC 연결
    client pbftv1.PBFTServiceClient    // gRPC 클라이언트
}

type GRPCTransportConfig struct {
    NodeID  string  // 노드 ID
    Address string  // 리슨 주소
}
```

#### TransportInterface
```go
// TransportInterface defines the interface for PBFT transport layer.
// This allows for different implementations (gRPC, TCP, mock).
type TransportInterface interface {
    Start() error
    Stop()
    AddPeer(nodeID, address string) error
    RemovePeer(nodeID string)
    Broadcast(msg *pbft.Message) error
    Send(nodeID string, msg *pbft.Message) error
    SetMessageHandler(handler func(*pbft.Message))
    GetPeers() []string
    PeerCount() int
}
```

---

### 1.6 Crypto 패키지 (crypto/)

#### KeyPair
```go
type KeyPair struct {
    PrivateKey *ecdsa.PrivateKey  // ECDSA P-256 개인키
    PublicKey  *ecdsa.PublicKey   // 공개키
}
```

#### Signature
```go
type Signature struct {
    R []byte  // ECDSA R 값
    S []byte  // ECDSA S 값
}
```

#### Signer / DefaultSigner
```go
type Signer interface {
    Sign(data []byte) (*Signature, error)
    Verify(data []byte, sig *Signature) bool
    PublicKeyBytes() []byte
}

type DefaultSigner struct {
    keyPair *KeyPair
}
```

---

### 1.7 Metrics 패키지 (metrics/)

#### Metrics (metrics/prometheus.go)
```go
type Metrics struct {
    mu sync.RWMutex

    // Consensus metrics
    consensusRoundsTotal   prometheus.Counter    // 총 합의 라운드
    consensusDuration      prometheus.Histogram  // 합의 소요 시간
    currentBlockHeight     prometheus.Gauge      // 현재 블록 높이
    currentView            prometheus.Gauge      // 현재 뷰 번호

    // Message metrics
    messagesSentTotal     *prometheus.CounterVec   // 타입별 전송 메시지 수
    messagesReceivedTotal *prometheus.CounterVec   // 타입별 수신 메시지 수
    messageProcessingTime *prometheus.HistogramVec // 메시지 처리 시간

    // View change metrics
    viewChangesTotal prometheus.Counter  // 뷰 변경 횟수

    // Block metrics
    blockExecutionTime prometheus.Histogram  // 블록 실행 시간
    transactionsTotal  prometheus.Counter    // 총 트랜잭션 수
    tps                prometheus.Gauge      // 초당 트랜잭션

    // Internal tracking
    roundStartTimes map[uint64]time.Time
    txCount         int64
    lastTpsUpdate   time.Time
}

type Server struct {
    addr   string
    server *http.Server
}
```

---

### 1.8 Node 패키지 (node/)

#### Node (node/node.go)
```go
type Node struct {
    mu sync.RWMutex

    config    *Config                   // 설정
    engine    *pbft.Engine              // PBFT 합의 엔진
    transport *transport.GRPCTransport  // P2P 통신
    mempool   *mempool.Mempool          // 트랜잭션 풀
    reactor   *mempool.Reactor          // Mempool 네트워크 리액터
    metrics   *metrics.Metrics          // 매트릭

    // State
    running bool            // 실행 상태
    done    chan struct{}   // 종료 채널

    // Logger
    logger *log.Logger

    // Metrics HTTP server
    metricsServer *http.Server
}

type Config struct {
    NodeID  string   // 노드 ID
    ChainID string   // 체인 ID

    ListenAddr string  // P2P 주소
    ABCIAddr   string  // ABCI 앱 주소

    Peers      []string            // 피어 목록
    Validators []*types.Validator  // 검증자 목록

    // 타이밍
    RequestTimeout     time.Duration
    ViewChangeTimeout  time.Duration
    CheckpointInterval uint64
    WindowSize         uint64

    // 메트릭
    MetricsEnabled bool
    MetricsAddr    string

    LogLevel string
    DataDir  string
}
```

---

### 1.9 gRPC 서비스 (api/pbft/v1/)

#### PBFTService
```go
// 서버 인터페이스
type PBFTServiceServer interface {
    // 메시지 브로드캐스트
    BroadcastMessage(context.Context, *BroadcastRequest) (*BroadcastResponse, error)
    // 특정 노드에 메시지 전송
    SendMessage(context.Context, *SendRequest) (*SendResponse, error)
    // 상태 동기화
    SyncState(context.Context, *SyncRequest) (*SyncResponse, error)
    // 체크포인트 조회
    GetCheckpoint(context.Context, *CheckpointRequest) (*CheckpointResponse, error)
    // 상태 조회
    GetStatus(context.Context, *StatusRequest) (*StatusResponse, error)
    // 양방향 스트림
    StreamMessages(PBFTService_StreamMessagesServer) error
}

// 클라이언트 인터페이스
type PBFTServiceClient interface {
    BroadcastMessage(ctx context.Context, in *BroadcastRequest) (*BroadcastResponse, error)
    SendMessage(ctx context.Context, in *SendRequest) (*SendResponse, error)
    SyncState(ctx context.Context, in *SyncRequest) (*SyncResponse, error)
    GetCheckpoint(ctx context.Context, in *CheckpointRequest) (*CheckpointResponse, error)
    GetStatus(ctx context.Context, in *StatusRequest) (*StatusResponse, error)
    StreamMessages(ctx context.Context) (PBFTService_StreamMessagesClient, error)
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
