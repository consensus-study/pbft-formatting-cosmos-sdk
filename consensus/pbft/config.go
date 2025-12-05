// Package pbft provides PBFT consensus configuration and interfaces.
package pbft

import (
	"time"

	"github.com/ahwlsqja/pbft-cosmos/types"
)

// PBFT 엔진 설정 구조체
type Config struct {
	// 노드 ID
	NodeID string

	// 요청 타임 아웃
	RequestTimeout time.Duration

	// 뷰 체인지 타임아웃 (10초)
	ViewChangeTimeout time.Duration

	// 체크포인트 주기 (100블록마다)
	CheckpointInterval uint64

	// 윈도우 크기 (200)
	WindowSize uint64
}

func DefaultConfig(nodeID string) *Config {
	return &Config{
		NodeID:             nodeID,
		RequestTimeout:     5 * time.Second,
		ViewChangeTimeout:  10 * time.Second,
		CheckpointInterval: 100,
		WindowSize:         200,
	}
}

// 구현은 다른 파일에서 하지만 "이런 기능이 필요하다" 정의
type Transport interface {
	// 모든 노드에게 전송
	Broadcast(msg *Message) error

	Send(nodeID string, msg *Message) error

	// SetMessageHandler sets the handler for incoming messages.
	SetMessageHandler(handler func(*Message))
}

// 앱에게 보내주는 것임 ABCI를 통해서
type Application interface {
	// 블록 실행
	ExecuteBlock(block *types.Block) ([]byte, error)

	// 블록 검증
	ValidateBlock(block *types.Block) error

	// 대기 중인 트랜잭션
	GetPendingTransactions() []types.Transaction

	// 블록 커밋
	Commit(block *types.Block) error
}
