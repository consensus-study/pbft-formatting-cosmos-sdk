# PBFT-Cosmos 코드 분석 문서

이 디렉토리는 PBFT-Cosmos 프로젝트의 전체 코드베이스에 대한 상세한 분석 문서를 포함합니다.

## 문서 목록

### 1. [타입 및 구조체 분석](01_TYPES_AND_STRUCTURES.md)
모든 패키지의 타입과 구조체 정의를 상세히 설명합니다.
- Consensus 패키지 타입 (MessageType, PBFTMessage, Block 등)
- ABCI 어댑터 타입
- Mempool 타입
- Network/Transport 타입
- Crypto 타입
- Metrics 타입
- Node 설정 타입
- 에러 타입

### 2. [합의 코드 플로우](02_CODE_FLOW_CONSENSUS.md)
PBFT 합의 알고리즘의 전체 흐름을 분석합니다.
- PBFT 합의 전체 흐름 (Request → Pre-Prepare → Prepare → Commit)
- 각 단계별 상세 코드 경로
- View Change 프로토콜
- Checkpoint 프로토콜
- 합의 상태 전이 다이어그램
- 쿼럼 요구사항

### 3. [트랜잭션 코드 플로우](03_CODE_FLOW_TRANSACTION.md)
트랜잭션의 전체 생명주기를 분석합니다.
- 트랜잭션 제출 플로우 (클라이언트 → 멤풀)
- 피어로부터 수신 플로우
- 멤풀 내부 처리 (검증, 저장, 우선순위 큐)
- 블록 제안 시 트랜잭션 수집
- 트랜잭션 실행 (FinalizeBlock, Commit)
- 트랜잭션 제거
- 브로드캐스트

### 4. [네트워크 코드 플로우](04_CODE_FLOW_NETWORK.md)
P2P 네트워킹 구현을 분석합니다.
- P2P 네트워크 아키텍처
- gRPC Transport 플로우 (서버 시작, 피어 연결, 메시지 송수신)
- TCP Transport 플로우 (대안 구현)
- 메시지 직렬화/역직렬화
- 네트워크 에러 처리
- 네트워크 메트릭

### 5. [ABCI 2.0 코드 플로우](05_CODE_FLOW_ABCI.md)
ABCI 2.0 인터페이스 구현을 분석합니다.
- ABCI 2.0 아키텍처 개요
- 메서드 호출 순서
- InitChain, CheckTx, PrepareProposal, ProcessProposal, FinalizeBlock, Commit 플로우
- Query 플로우
- ABCI 1.0 vs ABCI 2.0 비교

### 6. [노드 시작/실행 코드 플로우](06_CODE_FLOW_NODE.md)
노드의 초기화 및 실행을 분석합니다.
- 노드 아키텍처
- 노드 시작 플로우 (NewNode, Start)
- 노드 종료 플로우
- CLI 명령어 플로우 (start, status)
- 설정 로드
- 시그널 처리

### 7. [암호화 코드 플로우](07_CODE_FLOW_CRYPTO.md)
암호화 기능 구현을 분석합니다.
- 암호화 구성요소
- 키 쌍 생성 (ECDSA P-256)
- 서명 생성/검증
- 해시 함수 (SHA-256)
- 머클 트리
- PBFT 메시지 서명
- 블록 해시 생성

## 아키텍처 개요

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                         PBFT-COSMOS ARCHITECTURE                             │
│                                                                              │
│   ┌───────────────────────────────────────────────────────────────────────┐ │
│   │                              Application Layer                         │ │
│   │                                                                        │ │
│   │   ┌─────────────┐   ┌─────────────┐   ┌─────────────┐                 │ │
│   │   │   CLI (cmd) │   │  REST API   │   │   gRPC API  │                 │ │
│   │   └──────┬──────┘   └──────┬──────┘   └──────┬──────┘                 │ │
│   │          │                 │                 │                         │ │
│   └──────────┼─────────────────┼─────────────────┼─────────────────────────┘ │
│              │                 │                 │                           │
│   ┌──────────▼─────────────────▼─────────────────▼─────────────────────────┐ │
│   │                              Node Layer                                 │ │
│   │                                                                         │ │
│   │   ┌─────────────────────────────────────────────────────────────────┐  │ │
│   │   │                          Node                                    │  │ │
│   │   │                                                                  │  │ │
│   │   │  ┌──────────────────────────────────────────────────────────┐   │  │ │
│   │   │  │                    PBFT Engine                            │   │  │ │
│   │   │  │  (Pre-Prepare → Prepare → Commit → View Change)          │   │  │ │
│   │   │  └──────────────────────────────────────────────────────────┘   │  │ │
│   │   │                              │                                   │  │ │
│   │   │  ┌──────────────┬────────────┼────────────┬──────────────────┐  │  │ │
│   │   │  │              │            │            │                  │  │  │ │
│   │   │  │   Mempool    │   ABCI     │  Transport │    Metrics       │  │  │ │
│   │   │  │   + Reactor  │  Adapter   │   (gRPC)   │   (Prometheus)   │  │  │ │
│   │   │  │              │            │            │                  │  │  │ │
│   │   │  └──────────────┴────────────┴────────────┴──────────────────┘  │  │ │
│   │   │                                                                  │  │ │
│   │   └─────────────────────────────────────────────────────────────────┘  │ │
│   │                                                                         │ │
│   └─────────────────────────────────────────────────────────────────────────┘ │
│                                          │                                    │
│   ┌──────────────────────────────────────▼────────────────────────────────┐  │
│   │                           External Services                            │  │
│   │                                                                        │  │
│   │   ┌────────────────────────┐        ┌────────────────────────┐        │  │
│   │   │    Cosmos SDK App      │        │     Other PBFT Nodes   │        │  │
│   │   │    (ABCI Server)       │        │     (P2P Network)      │        │  │
│   │   └────────────────────────┘        └────────────────────────┘        │  │
│   │                                                                        │  │
│   └────────────────────────────────────────────────────────────────────────┘  │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

## 패키지 의존성

```
cmd/pbftd
    └── node
         ├── consensus/pbft (Engine, ABCIAdapter)
         │    ├── abci (Client, Types)
         │    ├── types (Block, Transaction, Validator)
         │    └── crypto (Signer, Hash, MerkleRoot)
         ├── mempool (Mempool, Reactor)
         ├── transport (GRPCTransport)
         ├── metrics (Prometheus)
         └── api/pbft/v1 (Protobuf, gRPC)
```

## 주요 코드 경로

| 기능 | 시작점 | 주요 파일 |
|------|--------|----------|
| 노드 시작 | `cmd/pbftd/main.go` | node/node.go |
| 트랜잭션 제출 | `node.SubmitTx()` | mempool/reactor.go → mempool/mempool.go |
| 블록 제안 | `engine.proposeBlock()` | consensus/pbft/engine.go |
| 합의 수행 | `engine.handleMessage()` | consensus/pbft/engine.go |
| ABCI 통신 | `abciAdapter.*()` | consensus/pbft/abci_adapter.go → abci/client.go |
| P2P 통신 | `transport.*()` | transport/grpc.go |

## 참고 자료

- [PBFT 원본 논문](https://pmg.csail.mit.edu/papers/osdi99.pdf)
- [CometBFT ABCI 2.0 문서](https://docs.cometbft.com/v0.38/spec/abci/)
- [Cosmos SDK 문서](https://docs.cosmos.network/)
