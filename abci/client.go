// Package abci provides ABCI 2.0 client for CometBFT v0.38.x
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

// Client - ABCI 클라이언트 (PBFT 엔진 → Cosmos SDK 앱 통신)
type Client struct {
	mu     sync.RWMutex
	conn   *grpc.ClientConn
	client abci.ABCIClient

	// 앱 정보 캐시
	lastHeight  int64
	lastAppHash []byte

	// 설정
	address string
	timeout time.Duration
}

// ClientConfig - 클라이언트 설정
type ClientConfig struct {
	Address string
	Timeout time.Duration
}

// DefaultClientConfig - 기본 설정
func DefaultClientConfig(address string) *ClientConfig {
	return &ClientConfig{
		Address: address,
		Timeout: 10 * time.Second,
	}
}

// NewClient - ABCI 클라이언트 생성
func NewClient(config *ClientConfig) (*Client, error) {
	ctx, cancel := context.WithTimeout(context.Background(), config.Timeout)
	defer cancel()

	conn, err := grpc.DialContext(
		ctx,
		config.Address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to ABCI app at %s: %w", config.Address, err)
	}

	return &Client{
		conn:    conn,
		client:  abci.NewABCIClient(conn),
		address: config.Address,
		timeout: config.Timeout,
	}, nil
}

// Close - 연결 종료
func (c *Client) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// Info - 앱 정보 조회
func (c *Client) Info(ctx context.Context) (*abci.ResponseInfo, error) {
	return c.client.Info(ctx, &abci.RequestInfo{})
}

// InitChain - 체인 초기화
func (c *Client) InitChain(ctx context.Context, req *abci.RequestInitChain) (*abci.ResponseInitChain, error) {
	resp, err := c.client.InitChain(ctx, req)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	c.lastAppHash = resp.AppHash
	c.mu.Unlock()

	return resp, nil
}

// CheckTx - 트랜잭션 검증 (멤풀 진입 전)
func (c *Client) CheckTx(ctx context.Context, tx []byte) (*abci.ResponseCheckTx, error) {
	return c.client.CheckTx(ctx, &abci.RequestCheckTx{
		Tx:   tx,
		Type: abci.CheckTxType_New,
	})
}

// CheckTxRecheck - 기존 멤풀 트랜잭션 재검증
func (c *Client) CheckTxRecheck(ctx context.Context, tx []byte) (*abci.ResponseCheckTx, error) {
	return c.client.CheckTx(ctx, &abci.RequestCheckTx{
		Tx:   tx,
		Type: abci.CheckTxType_Recheck,
	})
}

// PrepareProposal - 블록 제안 준비 (리더만 호출)
// ABCI 앱에게 트랜잭션 정렬/필터링 요청
func (c *Client) PrepareProposal(ctx context.Context, req *abci.RequestPrepareProposal) (*abci.ResponsePrepareProposal, error) {
	return c.client.PrepareProposal(ctx, req)
}

// ProcessProposal - 블록 제안 검증 (모든 노드)
// 리턴값: ACCEPT, REJECT, UNKNOWN
func (c *Client) ProcessProposal(ctx context.Context, req *abci.RequestProcessProposal) (*abci.ResponseProcessProposal, error) {
	return c.client.ProcessProposal(ctx, req)
}

// FinalizeBlock - 블록 실행 (ABCI 2.0: BeginBlock + DeliverTx + EndBlock 통합)
func (c *Client) FinalizeBlock(ctx context.Context, req *abci.RequestFinalizeBlock) (*abci.ResponseFinalizeBlock, error) {
	return c.client.FinalizeBlock(ctx, req)
}

// Commit - 상태 커밋
func (c *Client) Commit(ctx context.Context) (*abci.ResponseCommit, error) {
	resp, err := c.client.Commit(ctx, &abci.RequestCommit{})
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	c.lastHeight++
	c.mu.Unlock()

	return resp, nil
}

// ExtendVote - 투표 확장 (선택적, PBFT에서는 보통 사용 안 함)
func (c *Client) ExtendVote(ctx context.Context, req *abci.RequestExtendVote) (*abci.ResponseExtendVote, error) {
	return c.client.ExtendVote(ctx, req)
}

// VerifyVoteExtension - 투표 확장 검증
func (c *Client) VerifyVoteExtension(ctx context.Context, req *abci.RequestVerifyVoteExtension) (*abci.ResponseVerifyVoteExtension, error) {
	return c.client.VerifyVoteExtension(ctx, req)
}

// Query - 상태 쿼리
func (c *Client) Query(ctx context.Context, req *abci.RequestQuery) (*abci.ResponseQuery, error) {
	return c.client.Query(ctx, req)
}

// ListSnapshots - 스냅샷 목록
func (c *Client) ListSnapshots(ctx context.Context) (*abci.ResponseListSnapshots, error) {
	return c.client.ListSnapshots(ctx, &abci.RequestListSnapshots{})
}

// OfferSnapshot - 스냅샷 제공
func (c *Client) OfferSnapshot(ctx context.Context, req *abci.RequestOfferSnapshot) (*abci.ResponseOfferSnapshot, error) {
	return c.client.OfferSnapshot(ctx, req)
}

// LoadSnapshotChunk - 스냅샷 청크 로드
func (c *Client) LoadSnapshotChunk(ctx context.Context, req *abci.RequestLoadSnapshotChunk) (*abci.ResponseLoadSnapshotChunk, error) {
	return c.client.LoadSnapshotChunk(ctx, req)
}

// ApplySnapshotChunk - 스냅샷 청크 적용
func (c *Client) ApplySnapshotChunk(ctx context.Context, req *abci.RequestApplySnapshotChunk) (*abci.ResponseApplySnapshotChunk, error) {
	return c.client.ApplySnapshotChunk(ctx, req)
}

// GetLastHeight - 마지막 높이 반환
func (c *Client) GetLastHeight() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lastHeight
}

// GetLastAppHash - 마지막 앱 해시 반환
func (c *Client) GetLastAppHash() []byte {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lastAppHash
}

// SetLastHeight - 높이 설정
func (c *Client) SetLastHeight(height int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastHeight = height
}

// SetLastAppHash - 앱 해시 설정
func (c *Client) SetLastAppHash(hash []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastAppHash = hash
}

// IsConnected - 연결 상태 확인
func (c *Client) IsConnected() bool {
	return c.conn != nil
}

// GetAddress - 연결 주소 반환
func (c *Client) GetAddress() string {
	return c.address
}
