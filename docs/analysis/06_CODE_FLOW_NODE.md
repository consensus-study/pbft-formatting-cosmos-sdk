# 노드 시작/실행 코드 플로우 분석

## 1. 노드 아키텍처

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                           NODE ARCHITECTURE                                  │
│                                                                              │
│   ┌─────────────────────────────────────────────────────────────────────┐   │
│   │                              Node                                    │   │
│   │                                                                      │   │
│   │   ┌─────────────┐   ┌─────────────┐   ┌─────────────┐              │   │
│   │   │   Config    │   │   Metrics   │   │   Logger    │              │   │
│   │   └─────────────┘   └─────────────┘   └─────────────┘              │   │
│   │                                                                      │   │
│   │   ┌──────────────────────────────────────────────────────────────┐ │   │
│   │   │                    PBFT Engine                                │ │   │
│   │   │                                                               │ │   │
│   │   │  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐    │ │   │
│   │   │  │  State   │  │  Log     │  │  Timer   │  │ Validator│    │ │   │
│   │   │  │  Machine │  │  Store   │  │  Manager │  │   Set    │    │ │   │
│   │   │  └──────────┘  └──────────┘  └──────────┘  └──────────┘    │ │   │
│   │   │                                                               │ │   │
│   │   └──────────────────────────────────────────────────────────────┘ │   │
│   │                              │                                       │   │
│   │   ┌──────────────────────────┼──────────────────────────────────┐ │   │
│   │   │                          │                                    │ │   │
│   │   │  ┌─────────────┐   ┌─────▼─────┐   ┌─────────────┐          │ │   │
│   │   │  │   Mempool   │   │  ABCI     │   │  Transport  │          │ │   │
│   │   │  │   + Reactor │   │  Adapter  │   │   (gRPC)    │          │ │   │
│   │   │  └─────────────┘   └───────────┘   └─────────────┘          │ │   │
│   │   │                                                               │ │   │
│   │   └──────────────────────────────────────────────────────────────┘ │   │
│   │                                                                      │   │
│   └─────────────────────────────────────────────────────────────────────┘   │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

## 2. 노드 시작 플로우

### 2.1 전체 시작 순서

```
┌─────────────────────────────────────────────────────────────────┐
│                  NODE START SEQUENCE                             │
│                                                                  │
│   1. LoadConfig                                                  │
│      └── 설정 파일 로드, 검증                                      │
│                                                                  │
│   2. NewNode (또는 NewNodeWithABCI)                              │
│      ├── Config 검증                                             │
│      ├── ABCI Adapter 생성                                       │
│      ├── Mempool 생성                                            │
│      ├── Reactor 생성                                            │
│      ├── Transport 생성                                          │
│      ├── PBFT Engine 생성                                        │
│      └── Metrics 서버 생성                                        │
│                                                                  │
│   3. Start()                                                     │
│      ├── Transport 시작                                          │
│      ├── 피어 연결                                                │
│      ├── Mempool 시작                                            │
│      ├── Reactor 시작                                            │
│      ├── PBFT Engine 시작                                        │
│      └── Metrics 서버 시작                                        │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

### 2.2 NewNode 상세 플로우

```
┌─────────────────────────────────────────────────────────────────┐
│                      NEW NODE                                    │
│                                                                  │
│  main            Node                  Components                │
│    │               │                       │                     │
│    │ NewNode(cfg)  │                       │                     │
│    │──────────────►│                       │                     │
│    │               │                       │                     │
│    │               │ Validate config       │                     │
│    │               │───────┐               │                     │
│    │               │       │               │                     │
│    │               │◄──────┘               │                     │
│    │               │                       │                     │
│    │               │ NewABCIAdapter        │                     │
│    │               │──────────────────────►│                     │
│    │               │                       │                     │
│    │               │ NewMempool            │                     │
│    │               │──────────────────────►│                     │
│    │               │                       │                     │
│    │               │ NewReactor            │                     │
│    │               │──────────────────────►│                     │
│    │               │                       │                     │
│    │               │ NewGRPCTransport      │                     │
│    │               │──────────────────────►│                     │
│    │               │                       │                     │
│    │               │ NewPBFTEngine         │                     │
│    │               │──────────────────────►│                     │
│    │               │                       │                     │
│    │               │ NewMetrics            │                     │
│    │               │──────────────────────►│                     │
│    │               │                       │                     │
│    │◄──────────────│                       │                     │
│    │   *Node       │                       │                     │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

**코드 경로:**
```go
// node/node.go - NewNode() / NewNodeWithABCI()
func NewNode(config *Config) (*Node, error) {
    // 설정 검증
    if err := config.Validate(); err != nil {
        return nil, fmt.Errorf("invalid config: %w", err)
    }

    // gRPC Transport 생성
    trans, err := transport.NewGRPCTransport(config.NodeID, config.ListenAddr)
    if err != nil {
        return nil, fmt.Errorf("failed to create transport: %w", err)
    }

    // Validator Set 생성
    validatorSet := types.NewValidatorSet(config.Validators)

    // PBFT Configuration 생성
    pbftConfig := &pbft.Config{
        NodeID:             config.NodeID,
        RequestTimeout:     config.RequestTimeout,
        ViewChangeTimeout:  config.ViewChangeTimeout,
        CheckpointInterval: config.CheckpointInterval,
        WindowSize:         config.WindowSize,
    }

    // 메트릭 생성
    var m *metrics.Metrics
    if config.MetricsEnabled {
        m = metrics.NewMetrics("pbft")
    }

    // PBFT Engine 생성
    engine := pbft.NewEngine(pbftConfig, validatorSet, trans, nil, m)

    return &Node{
        config:    config,
        engine:    engine,
        transport: trans,
        metrics:   m,
        done:      make(chan struct{}),
        logger:    log.Default(),
    }, nil
}
```

### 2.3 Start() 상세 플로우

```
┌─────────────────────────────────────────────────────────────────┐
│                      NODE START                                  │
│                                                                  │
│  main         Node        Transport    Engine    Metrics        │
│    │           │              │           │          │           │
│    │ Start()   │              │           │          │           │
│    │──────────►│              │           │          │           │
│    │           │              │           │          │           │
│    │           │ Start()      │           │          │           │
│    │           │─────────────►│           │          │           │
│    │           │              │           │          │           │
│    │           │              │ Listen    │          │           │
│    │           │              │───────┐   │          │           │
│    │           │              │       │   │          │           │
│    │           │              │◄──────┘   │          │           │
│    │           │              │           │          │           │
│    │           │◄─────────────│           │          │           │
│    │           │              │           │          │           │
│    │           │ AddPeers     │           │          │           │
│    │           │─────────────►│           │          │           │
│    │           │              │           │          │           │
│    │           │              │ Connect   │          │           │
│    │           │              │───────┐   │          │           │
│    │           │              │       │   │          │           │
│    │           │              │◄──────┘   │          │           │
│    │           │              │           │          │           │
│    │           │◄─────────────│           │          │           │
│    │           │              │           │          │           │
│    │           │ Start()      │           │          │           │
│    │           │──────────────────────────►          │           │
│    │           │              │           │          │           │
│    │           │◄─────────────────────────│          │           │
│    │           │              │           │          │           │
│    │           │ Start()      │           │          │           │
│    │           │───────────────────────────────────►│           │
│    │           │              │           │          │           │
│    │           │◄──────────────────────────────────│           │
│    │           │              │           │          │           │
│    │◄──────────│              │           │          │           │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

**코드 경로:**
```go
// node/node.go - Start()
func (n *Node) Start(ctx context.Context) error {
    n.mu.Lock()
    if n.running {
        n.mu.Unlock()
        return fmt.Errorf("node already running")
    }
    n.running = true
    n.mu.Unlock()

    n.logger.Printf("[Node] Starting PBFT node %s", n.config.NodeID)

    // 1. Transport 시작
    if err := n.transport.Start(); err != nil {
        return fmt.Errorf("failed to start transport: %w", err)
    }
    n.logger.Printf("[Node] Transport started on %s", n.config.ListenAddr)

    // 2. 피어 연결 (형식: nodeID@address)
    for _, peerStr := range n.config.Peers {
        parts := strings.SplitN(peerStr, "@", 2)
        if len(parts) != 2 {
            n.logger.Printf("[Node] Invalid peer format: %s", peerStr)
            continue
        }
        peerID, peerAddr := parts[0], parts[1]

        // 자기 자신은 건너뜀
        if peerID == n.config.NodeID {
            continue
        }

        if err := n.transport.AddPeer(peerID, peerAddr); err != nil {
            n.logger.Printf("[Node] Failed to connect to peer %s: %v", peerID, err)
        } else {
            n.logger.Printf("[Node] Connected to peer %s at %s", peerID, peerAddr)
        }
    }

    // 3. Metrics 서버 시작 (별도 고루틴)
    if n.config.MetricsEnabled && n.metrics != nil {
        go n.startMetricsServer()
    }

    // 4. PBFT Engine 시작
    if err := n.engine.Start(); err != nil {
        return fmt.Errorf("failed to start engine: %w", err)
    }

    n.logger.Printf("[Node] PBFT node %s started successfully", n.config.NodeID)
    return nil
}
```

## 3. 노드 종료 플로우

```
┌─────────────────────────────────────────────────────────────────┐
│                      NODE STOP                                   │
│                                                                  │
│  main         Node        Engine    Transport   Mempool         │
│    │           │            │           │          │             │
│    │ Stop()    │            │           │          │             │
│    │──────────►│            │           │          │             │
│    │           │            │           │          │             │
│    │           │ cancel()   │           │          │             │
│    │           │───────┐    │           │          │             │
│    │           │       │    │           │          │             │
│    │           │◄──────┘    │           │          │             │
│    │           │            │           │          │             │
│    │           │ Stop()     │           │          │             │
│    │           │───────────►│           │          │             │
│    │           │            │           │          │             │
│    │           │◄───────────│           │          │             │
│    │           │            │           │          │             │
│    │           │ Stop()     │           │          │             │
│    │           │────────────────────────►          │             │
│    │           │            │           │          │             │
│    │           │◄───────────────────────│          │             │
│    │           │            │           │          │             │
│    │           │ Stop()     │           │          │             │
│    │           │───────────────────────────────────►             │
│    │           │            │           │          │             │
│    │           │◄──────────────────────────────────│             │
│    │           │            │           │          │             │
│    │           │ Close ABCI │           │          │             │
│    │           │───────┐    │           │          │             │
│    │           │       │    │           │          │             │
│    │           │◄──────┘    │           │          │             │
│    │           │            │           │          │             │
│    │◄──────────│            │           │          │             │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

**코드 경로:**
```go
// node/node.go - Stop()
func (n *Node) Stop() error {
    n.mu.Lock()
    if !n.running {
        n.mu.Unlock()
        return nil
    }
    n.running = false
    n.mu.Unlock()

    n.logger.Printf("[Node] Stopping PBFT node %s", n.config.NodeID)

    // done 채널 닫기
    close(n.done)

    // 1. Metrics 서버 종료
    if n.metricsServer != nil {
        n.metricsServer.Close()
    }

    // 2. PBFT Engine 종료
    n.engine.Stop()

    // 3. Transport 종료
    n.transport.Stop()

    n.logger.Printf("[Node] PBFT node %s stopped", n.config.NodeID)
    return nil
}
```

## 4. CLI 명령어 플로우

### 4.1 start 명령어

```
┌─────────────────────────────────────────────────────────────────┐
│                   CMD START FLOW                                 │
│                                                                  │
│   User             Cobra            Node                        │
│     │                │                │                          │
│     │ pbftd start    │                │                          │
│     │───────────────►│                │                          │
│     │                │                │                          │
│     │                │ runStart()     │                          │
│     │                │───────┐        │                          │
│     │                │       │        │                          │
│     │                │◄──────┘        │                          │
│     │                │                │                          │
│     │                │ LoadConfig()   │                          │
│     │                │───────┐        │                          │
│     │                │       │        │                          │
│     │                │◄──────┘        │                          │
│     │                │                │                          │
│     │                │ NewNode()      │                          │
│     │                │───────────────►│                          │
│     │                │                │                          │
│     │                │◄───────────────│                          │
│     │                │                │                          │
│     │                │ Start()        │                          │
│     │                │───────────────►│                          │
│     │                │                │                          │
│     │                │◄───────────────│                          │
│     │                │                │                          │
│     │                │ WaitSignal()   │                          │
│     │                │───────┐        │                          │
│     │                │       │        │                          │
│     │                │◄──────┘        │                          │
│     │                │                │                          │
│     │◄───────────────│                │                          │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

**코드 경로:**
```go
// cmd/pbftd/main.go - runStart()
func runStart(cmd *cobra.Command, args []string) error {
    // 설정 로드
    config, err := loadConfig()
    if err != nil {
        return fmt.Errorf("failed to load config: %w", err)
    }

    // 노드 생성
    node, err := node.NewNodeWithABCI(config)
    if err != nil {
        return fmt.Errorf("failed to create node: %w", err)
    }

    // 노드 시작
    if err := node.Start(); err != nil {
        return fmt.Errorf("failed to start node: %w", err)
    }

    fmt.Printf("PBFT node %s started\n", config.NodeID)

    // 시그널 대기
    sigCh := make(chan os.Signal, 1)
    signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
    <-sigCh

    fmt.Println("Shutting down...")

    // 정상 종료
    if err := node.Stop(); err != nil {
        return fmt.Errorf("failed to stop node: %w", err)
    }

    return nil
}
```

### 4.2 status 명령어

```go
// cmd/pbftd/main.go - runStatus()
func runStatus(cmd *cobra.Command, args []string) error {
    // 노드에 연결
    addr := cmd.Flag("addr").Value.String()
    conn, err := grpc.Dial(addr, grpc.WithInsecure())
    if err != nil {
        return fmt.Errorf("failed to connect: %w", err)
    }
    defer conn.Close()

    client := pbftv1.NewPBFTServiceClient(conn)

    // 상태 조회
    resp, err := client.GetStatus(context.Background(),
        &pbftv1.StatusRequest{})
    if err != nil {
        return fmt.Errorf("failed to get status: %w", err)
    }

    // 출력
    fmt.Printf("Node ID: %s\n", resp.NodeId)
    fmt.Printf("View: %d\n", resp.View)
    fmt.Printf("Sequence: %d\n", resp.Sequence)
    fmt.Printf("Height: %d\n", resp.Height)
    fmt.Printf("Peers: %d\n", resp.PeerCount)

    return nil
}
```

## 5. 설정 로드 플로우

```go
// cmd/pbftd/main.go - loadConfig()
func loadConfig() (*node.Config, error) {
    config := node.DefaultConfig()

    // 플래그에서 설정 로드
    config.NodeID = viper.GetString("node-id")
    config.ChainID = viper.GetString("chain-id")
    config.ListenAddr = viper.GetString("listen")
    config.ABCIAddr = viper.GetString("abci")

    // 피어 목록
    peers := viper.GetStringSlice("peers")
    config.Peers = peers

    // 검증자 목록 로드
    validatorsFile := viper.GetString("validators")
    if validatorsFile != "" {
        validators, err := loadValidatorsFromFile(validatorsFile)
        if err != nil {
            return nil, err
        }
        config.Validators = validators
    } else {
        // 기본 검증자 생성 (테스트용)
        config.Validators = createDefaultValidators()
    }

    // 타이밍 설정
    config.RequestTimeout = viper.GetDuration("request-timeout")
    config.ViewChangeTimeout = viper.GetDuration("view-change-timeout")

    // 메트릭 설정
    config.MetricsEnabled = viper.GetBool("metrics")
    config.MetricsAddr = viper.GetString("metrics-addr")

    return config, nil
}

// 검증자 파일 로드
func loadValidatorsFromFile(path string) ([]*types.Validator, error) {
    data, err := os.ReadFile(path)
    if err != nil {
        return nil, err
    }

    var validators []*types.Validator
    if err := json.Unmarshal(data, &validators); err != nil {
        return nil, err
    }

    return validators, nil
}
```

## 6. 시그널 처리

```go
// 정상 종료 시그널 처리
func handleSignals(node *Node) {
    sigCh := make(chan os.Signal, 1)
    signal.Notify(sigCh,
        syscall.SIGINT,  // Ctrl+C
        syscall.SIGTERM, // kill
        syscall.SIGHUP,  // 터미널 종료
    )

    go func() {
        sig := <-sigCh
        fmt.Printf("\nReceived signal: %v\n", sig)

        // 정상 종료 시작
        ctx, cancel := context.WithTimeout(
            context.Background(),
            30*time.Second,
        )
        defer cancel()

        // 노드 종료
        if err := node.Stop(); err != nil {
            fmt.Printf("Error stopping node: %v\n", err)
            os.Exit(1)
        }

        // 컨텍스트 타임아웃 체크
        select {
        case <-ctx.Done():
            fmt.Println("Shutdown timeout, forcing exit")
            os.Exit(1)
        default:
            fmt.Println("Shutdown complete")
            os.Exit(0)
        }
    }()
}
```
