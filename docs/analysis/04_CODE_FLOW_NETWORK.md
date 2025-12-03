# 네트워크 코드 플로우 분석

## 1. P2P 네트워크 아키텍처

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                       P2P NETWORK ARCHITECTURE                               │
│                                                                              │
│                            ┌───────────────┐                                │
│                            │    Node 0     │                                │
│                            │   (Leader)    │                                │
│                            └───────┬───────┘                                │
│                                    │                                         │
│               ┌────────────────────┼────────────────────┐                   │
│               │                    │                    │                   │
│        ┌──────▼──────┐      ┌──────▼──────┐      ┌──────▼──────┐           │
│        │   Node 1    │      │   Node 2    │      │   Node 3    │           │
│        │  (Replica)  │◄────►│  (Replica)  │◄────►│  (Replica)  │           │
│        └─────────────┘      └─────────────┘      └─────────────┘           │
│                                                                              │
│   Full mesh topology: 모든 노드가 서로 직접 연결                              │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

## 2. gRPC Transport 플로우

### 2.1 서버 시작

```
┌─────────────────────────────────────────────────────────────────┐
│                  GRPC SERVER START                               │
│                                                                  │
│  Node              GRPCTransport          gRPC Server           │
│    │                    │                      │                 │
│    │ Start()            │                      │                 │
│    │───────────────────►│                      │                 │
│    │                    │                      │                 │
│    │                    │ NewListener()        │                 │
│    │                    │─────────────────────►│                 │
│    │                    │                      │                 │
│    │                    │ RegisterService()    │                 │
│    │                    │─────────────────────►│                 │
│    │                    │                      │                 │
│    │                    │ go Serve()           │                 │
│    │                    │─────────────────────►│                 │
│    │                    │                      │                 │
│    │◄───────────────────│                      │                 │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

**코드 경로:**
```go
// transport/grpc.go - Start()
func (t *GRPCTransport) Start() error {
    t.mu.Lock()
    defer t.mu.Unlock()

    if t.isRunning {
        return nil
    }

    // 리스너 생성
    lis, err := net.Listen("tcp", t.config.ListenAddr)
    if err != nil {
        return fmt.Errorf("failed to listen: %w", err)
    }
    t.listener = lis

    // gRPC 서버 생성
    t.server = grpc.NewServer(
        grpc.MaxRecvMsgSize(t.config.MaxMessageSize),
        grpc.MaxSendMsgSize(t.config.MaxMessageSize),
    )

    // 서비스 등록
    pbftv1.RegisterPBFTServiceServer(t.server, t)

    // 서버 시작 (고루틴)
    go func() {
        if err := t.server.Serve(lis); err != nil {
            fmt.Printf("[Transport] Server error: %v\n", err)
        }
    }()

    t.isRunning = true
    return nil
}
```

### 2.2 피어 연결

```
┌─────────────────────────────────────────────────────────────────┐
│                  ADD PEER CONNECTION                             │
│                                                                  │
│  Node          GRPCTransport       gRPC Client      Remote      │
│    │                │                   │              │         │
│    │ AddPeer(addr)  │                   │              │         │
│    │───────────────►│                   │              │         │
│    │                │                   │              │         │
│    │                │ Dial()            │              │         │
│    │                │──────────────────►│              │         │
│    │                │                   │              │         │
│    │                │                   │ Connect      │         │
│    │                │                   │─────────────►│         │
│    │                │                   │              │         │
│    │                │                   │◄─────────────│         │
│    │                │                   │ Connected    │         │
│    │                │                   │              │         │
│    │                │◄──────────────────│              │         │
│    │                │  connection       │              │         │
│    │                │                   │              │         │
│    │                │ StreamMessages()  │              │         │
│    │                │──────────────────►│              │         │
│    │                │                   │              │         │
│    │                │ go receiveLoop()  │              │         │
│    │                │───────┐           │              │         │
│    │                │       │           │              │         │
│    │                │◄──────┘           │              │         │
│    │                │                   │              │         │
│    │◄───────────────│                   │              │         │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

**코드 경로:**
```go
// transport/grpc.go - AddPeer()
func (t *GRPCTransport) AddPeer(peerID, addr string) error {
    t.mu.Lock()
    defer t.mu.Unlock()

    // 이미 연결된 피어인지 확인
    if _, exists := t.peers[peerID]; exists {
        return nil
    }

    // gRPC 연결 설정
    ctx, cancel := context.WithTimeout(t.ctx, t.config.DialTimeout)
    defer cancel()

    conn, err := grpc.DialContext(ctx, addr,
        grpc.WithTransportCredentials(insecure.NewCredentials()),
        grpc.WithBlock(),
    )
    if err != nil {
        return fmt.Errorf("failed to connect to %s: %w", addr, err)
    }

    // 클라이언트 생성
    client := pbftv1.NewPBFTServiceClient(conn)

    // 양방향 스트림 설정
    stream, err := client.StreamMessages(t.ctx)
    if err != nil {
        conn.Close()
        return fmt.Errorf("failed to create stream: %w", err)
    }

    // 피어 연결 저장
    peer := &peerConn{
        id:     peerID,
        addr:   addr,
        conn:   conn,
        client: client,
        stream: stream,
    }
    t.peers[peerID] = peer

    // 수신 루프 시작
    go t.receiveLoop(peer)

    return nil
}
```

### 2.3 메시지 브로드캐스트

```
┌─────────────────────────────────────────────────────────────────┐
│                  BROADCAST MESSAGE                               │
│                                                                  │
│  Engine         GRPCTransport        Peer1   Peer2   Peer3      │
│    │                 │                 │       │       │         │
│    │ Broadcast(msg)  │                 │       │       │         │
│    │────────────────►│                 │       │       │         │
│    │                 │                 │       │       │         │
│    │                 │ for each peer:  │       │       │         │
│    │                 │                 │       │       │         │
│    │                 │ Send(msg)       │       │       │         │
│    │                 │────────────────►│       │       │         │
│    │                 │                 │       │       │         │
│    │                 │ Send(msg)       │       │       │         │
│    │                 │────────────────────────►│       │         │
│    │                 │                 │       │       │         │
│    │                 │ Send(msg)       │       │       │         │
│    │                 │────────────────────────────────►│         │
│    │                 │                 │       │       │         │
│    │◄────────────────│                 │       │       │         │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

**코드 경로:**
```go
// transport/grpc.go - Broadcast()
func (t *GRPCTransport) Broadcast(msg *pbftv1.PBFTMessage) error {
    t.mu.RLock()
    peers := make([]*peerConn, 0, len(t.peers))
    for _, p := range t.peers {
        peers = append(peers, p)
    }
    t.mu.RUnlock()

    var wg sync.WaitGroup
    errCh := make(chan error, len(peers))

    for _, peer := range peers {
        wg.Add(1)
        go func(p *peerConn) {
            defer wg.Done()
            if err := p.stream.Send(msg); err != nil {
                errCh <- fmt.Errorf("send to %s failed: %w", p.id, err)
            }
        }(peer)
    }

    wg.Wait()
    close(errCh)

    // 에러 수집
    var errs []error
    for err := range errCh {
        errs = append(errs, err)
    }

    if len(errs) > 0 {
        return fmt.Errorf("broadcast errors: %v", errs)
    }

    return nil
}
```

### 2.4 메시지 수신

```
┌─────────────────────────────────────────────────────────────────┐
│                  RECEIVE MESSAGE                                 │
│                                                                  │
│  Remote        Stream        GRPCTransport       Engine         │
│    │             │                │                 │            │
│    │ Send(msg)   │                │                 │            │
│    │────────────►│                │                 │            │
│    │             │                │                 │            │
│    │             │ Recv()         │                 │            │
│    │             │───────────────►│                 │            │
│    │             │                │                 │            │
│    │             │                │ messageHandler  │            │
│    │             │                │────────────────►│            │
│    │             │                │                 │            │
│    │             │                │                 │ Process    │
│    │             │                │                 │───────┐    │
│    │             │                │                 │       │    │
│    │             │                │                 │◄──────┘    │
│    │             │                │                 │            │
│    │             │                │◄────────────────│            │
│    │             │                │                 │            │
└─────────────────────────────────────────────────────────────────┘
```

**코드 경로:**
```go
// transport/grpc.go - receiveLoop()
func (t *GRPCTransport) receiveLoop(peer *peerConn) {
    for {
        select {
        case <-t.ctx.Done():
            return
        default:
        }

        msg, err := peer.stream.Recv()
        if err != nil {
            if err == io.EOF {
                fmt.Printf("[Transport] Peer %s disconnected\n", peer.id)
            } else {
                fmt.Printf("[Transport] Receive error from %s: %v\n",
                    peer.id, err)
            }
            t.removePeer(peer.id)
            return
        }

        // 메시지 핸들러 호출
        if t.messageHandler != nil {
            t.messageHandler(peer.id, msg)
        }
    }
}
```

## 3. TCP Transport 플로우 (대안)

### 3.1 연결 수락

```
┌─────────────────────────────────────────────────────────────────┐
│                  ACCEPT CONNECTION                               │
│                                                                  │
│  Remote           Listener         Transport                    │
│    │                 │                 │                         │
│    │ Connect         │                 │                         │
│    │────────────────►│                 │                         │
│    │                 │                 │                         │
│    │                 │ Accept()        │                         │
│    │                 │────────────────►│                         │
│    │                 │                 │                         │
│    │                 │                 │ handleConnection()      │
│    │                 │                 │───────┐                 │
│    │                 │                 │       │                 │
│    │                 │                 │       │ Handshake       │
│    │◄────────────────┼─────────────────┼───────┘                 │
│    │                 │                 │                         │
│    │ HandshakeResp   │                 │                         │
│    │────────────────►│                 │                         │
│    │                 │────────────────►│                         │
│    │                 │                 │                         │
│    │                 │                 │ addPeer()               │
│    │                 │                 │───────┐                 │
│    │                 │                 │       │                 │
│    │                 │                 │◄──────┘                 │
│    │                 │                 │                         │
└─────────────────────────────────────────────────────────────────┘
```

**코드 경로:**
```go
// network/transport.go - acceptLoop()
func (t *Transport) acceptLoop() {
    for {
        select {
        case <-t.ctx.Done():
            return
        default:
        }

        conn, err := t.listener.Accept()
        if err != nil {
            if t.isRunning {
                fmt.Printf("[Transport] Accept error: %v\n", err)
            }
            continue
        }

        go t.handleConnection(conn)
    }
}

// network/transport.go - handleConnection()
func (t *Transport) handleConnection(conn net.Conn) {
    // 핸드셰이크
    decoder := gob.NewDecoder(conn)
    encoder := gob.NewEncoder(conn)

    var handshake HandshakeMessage
    if err := decoder.Decode(&handshake); err != nil {
        conn.Close()
        return
    }

    // 응답
    response := HandshakeResponse{
        NodeID: t.nodeID,
        Accept: true,
    }
    if err := encoder.Encode(&response); err != nil {
        conn.Close()
        return
    }

    // 피어 추가
    peer := &Peer{
        ID:      handshake.NodeID,
        Addr:    conn.RemoteAddr().String(),
        conn:    conn,
        encoder: encoder,
        decoder: decoder,
    }

    t.addPeer(peer)
    go t.receiveLoop(peer)
}
```

## 4. 메시지 직렬화/역직렬화

### 4.1 Protobuf 메시지

```go
// api/pbft/v1/types.pb.go

// PBFTMessage - 모든 PBFT 메시지의 래퍼
message PBFTMessage {
    MessageType type = 1;
    uint64 view = 2;
    uint64 sequence = 3;
    bytes digest = 4;
    string node_id = 5;
    bytes signature = 6;
    bytes payload = 7;  // 실제 메시지 (PrePrepare, Prepare 등)
}

// 직렬화
func (m *PBFTMessage) Marshal() ([]byte, error) {
    return proto.Marshal(m)
}

// 역직렬화
func (m *PBFTMessage) Unmarshal(data []byte) error {
    return proto.Unmarshal(data, m)
}
```

### 4.2 gRPC 서비스 정의

```protobuf
// api/pbft/v1/service.proto

service PBFTService {
    // 메시지 브로드캐스트
    rpc BroadcastMessage(BroadcastRequest) returns (BroadcastResponse);

    // 특정 노드에 전송
    rpc SendMessage(SendRequest) returns (SendResponse);

    // 양방향 스트리밍 (메인 통신 채널)
    rpc MessageStream(stream PBFTMessage) returns (stream PBFTMessage);

    // 상태 동기화
    rpc SyncState(SyncStateRequest) returns (SyncStateResponse);

    // 체크포인트 조회
    rpc GetCheckpoint(GetCheckpointRequest) returns (GetCheckpointResponse);

    // 상태 조회
    rpc GetStatus(GetStatusRequest) returns (GetStatusResponse);
}
```

### 4.3 StateProvider 인터페이스

상태 동기화를 위해 GRPCTransport는 StateProvider 인터페이스를 통해 Engine으로부터 데이터를 제공받습니다:

```go
// transport/grpc.go - StateProvider
type StateProvider interface {
    // GetBlocksFromHeight 지정된 높이부터 블록들을 반환
    GetBlocksFromHeight(fromHeight uint64) []*types.Block
    // GetCheckpoints 저장된 체크포인트들을 반환
    GetCheckpoints() []pbft.Checkpoint
    // GetCheckpoint 특정 시퀀스 번호의 체크포인트 반환
    GetCheckpoint(seqNum uint64) (*pbft.Checkpoint, bool)
}

// GRPCTransport 구조체에 stateProvider 필드 추가
type GRPCTransport struct {
    // ...기존 필드들...
    stateProvider StateProvider
}

// SetStateProvider - StateProvider 설정
func (t *GRPCTransport) SetStateProvider(provider StateProvider) {
    t.mu.Lock()
    defer t.mu.Unlock()
    t.stateProvider = provider
}
```

### 4.4 SyncState/GetCheckpoint 구현

```go
// transport/grpc.go - SyncState()
func (t *GRPCTransport) SyncState(ctx context.Context,
    req *pbftv1.SyncStateRequest) (*pbftv1.SyncStateResponse, error) {

    t.mu.RLock()
    provider := t.stateProvider
    t.mu.RUnlock()

    if provider == nil {
        return &pbftv1.SyncStateResponse{
            Blocks:      []*pbftv1.Block{},
            Checkpoints: []*pbftv1.Checkpoint{},
        }, nil
    }

    // 요청된 높이부터 블록들 조회
    blocks := provider.GetBlocksFromHeight(req.FromHeight)
    // ... 변환 로직 ...

    return &pbftv1.SyncStateResponse{
        Blocks:      protoBlocks,
        Checkpoints: protoCheckpoints,
    }, nil
}

// transport/grpc.go - GetCheckpoint()
func (t *GRPCTransport) GetCheckpoint(ctx context.Context,
    req *pbftv1.GetCheckpointRequest) (*pbftv1.GetCheckpointResponse, error) {

    t.mu.RLock()
    provider := t.stateProvider
    t.mu.RUnlock()

    if provider == nil {
        return &pbftv1.GetCheckpointResponse{}, nil
    }

    checkpoint, found := provider.GetCheckpoint(req.SequenceNum)
    if !found {
        return &pbftv1.GetCheckpointResponse{}, nil
    }

    return &pbftv1.GetCheckpointResponse{
        Checkpoint: &pbftv1.Checkpoint{
            SequenceNum: checkpoint.SequenceNum,
            Digest:      checkpoint.Digest,
            NodeId:      checkpoint.NodeID,
        },
    }, nil
}
```

## 5. 네트워크 에러 처리

### 5.1 연결 실패

```
┌─────────────────────────────────────────────────────────────────┐
│              CONNECTION FAILURE HANDLING                         │
│                                                                  │
│  Transport              Peer                                     │
│      │                    │                                      │
│      │ AddPeer()          │                                      │
│      │───────────────────►│                                      │
│      │                    │                                      │
│      │                    │ Connection refused                   │
│      │◄───────────────────│                                      │
│      │                    │                                      │
│      │ Log error          │                                      │
│      │───────┐            │                                      │
│      │       │            │                                      │
│      │◄──────┘            │                                      │
│      │                    │                                      │
│      │ Schedule retry     │                                      │
│      │───────┐            │                                      │
│      │       │            │                                      │
│      │◄──────┘            │                                      │
│      │                    │                                      │
│      │ [After delay]      │                                      │
│      │ Retry connection   │                                      │
│      │───────────────────►│                                      │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

### 5.2 연결 끊김

```go
// transport/grpc.go - 연결 끊김 처리
func (t *GRPCTransport) receiveLoop(peer *peerConn) {
    for {
        msg, err := peer.stream.Recv()
        if err != nil {
            if err == io.EOF {
                // 정상적인 연결 종료
                fmt.Printf("[Transport] Peer %s disconnected\n", peer.id)
            } else {
                // 비정상 연결 끊김
                fmt.Printf("[Transport] Error from %s: %v\n", peer.id, err)
            }

            // 피어 제거
            t.removePeer(peer.id)

            // 재연결 시도 (옵션)
            go t.reconnectPeer(peer.id, peer.addr)

            return
        }

        // 메시지 처리
        if t.messageHandler != nil {
            t.messageHandler(peer.id, msg)
        }
    }
}

// 피어 제거
func (t *GRPCTransport) removePeer(peerID string) {
    t.mu.Lock()
    defer t.mu.Unlock()

    if peer, exists := t.peers[peerID]; exists {
        peer.conn.Close()
        delete(t.peers, peerID)
    }
}
```

## 6. 네트워크 메트릭

```go
// 전송 메트릭
type TransportMetrics struct {
    MessagesSent     prometheus.Counter    // 전송된 메시지 수
    MessagesReceived prometheus.Counter    // 수신된 메시지 수
    BytesSent        prometheus.Counter    // 전송된 바이트
    BytesReceived    prometheus.Counter    // 수신된 바이트
    PeerCount        prometheus.Gauge      // 연결된 피어 수
    ConnectionErrors prometheus.Counter    // 연결 에러 수
    SendLatency      prometheus.Histogram  // 전송 지연 시간
}

// 메트릭 업데이트 예시
func (t *GRPCTransport) Send(peerID string, msg *pbftv1.PBFTMessage) error {
    start := time.Now()

    // 전송
    err := t.sendToPeer(peerID, msg)

    // 메트릭 업데이트
    if t.metrics != nil {
        t.metrics.MessagesSent.Inc()
        t.metrics.SendLatency.Observe(time.Since(start).Seconds())
        if err != nil {
            t.metrics.ConnectionErrors.Inc()
        }
    }

    return err
}
```

## 7. 네트워크 설정

```go
// transport/grpc.go - GRPCTransportConfig
type GRPCTransportConfig struct {
    NodeID  string // 노드 ID
    Address string // 리슨 주소
}

// GRPCTransport 생성
func NewGRPCTransport(nodeID, address string) (*GRPCTransport, error) {
    return &GRPCTransport{
        nodeID:  nodeID,
        address: address,
        peers:   make(map[string]*peerConn),
        done:    make(chan struct{}),
    }, nil
}

// Start()에서 설정되는 gRPC 옵션
func (t *GRPCTransport) Start() error {
    // ...
    t.server = grpc.NewServer(
        grpc.MaxRecvMsgSize(64 * 1024 * 1024), // 64MB
        grpc.MaxSendMsgSize(64 * 1024 * 1024),
    )
    // ...
}

// AddPeer()에서 사용하는 연결 타임아웃
ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
```
