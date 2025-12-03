// Package transport provides gRPC-based P2P networking for PBFT consensus.
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
	"google.golang.org/protobuf/types/known/timestamppb"

	pbftv1 "github.com/ahwlsqja/pbft-cosmos/api/pbft/v1"
	"github.com/ahwlsqja/pbft-cosmos/consensus/pbft"
	"github.com/ahwlsqja/pbft-cosmos/types"
)

// StateProvider 상태 동기화를 위한 데이터 제공 인터페이스
type StateProvider interface {
	// GetBlocksFromHeight 지정된 높이부터 블록들을 반환
	GetBlocksFromHeight(fromHeight uint64) []*types.Block
	// GetCheckpoints 저장된 체크포인트들을 반환
	GetCheckpoints() []pbft.Checkpoint
	// GetCheckpoint 특정 시퀀스 번호의 체크포인트 반환
	GetCheckpoint(seqNum uint64) (*pbft.Checkpoint, bool)
}

// GRPCTransport 함축함 gRPC-based P2P communication for PBFT.
type GRPCTransport struct {
	mu sync.RWMutex

	nodeID   string // 노드 ID
	address  string // 리스너 주소
	server   *grpc.Server // gRPC 서버
	listener net.Listener // TCP 리스너

	// Peer connections
	peers map[string]*peerConn // nodeID -> peerConn

	// Message handler callback
	msgHandler func(*pbft.Message) // 메시지 핸들러

	// State provider for sync operations
	stateProvider StateProvider // 상태 동기화용 데이터 제공자

	// Running state
	running bool // 실행 상태
	done    chan struct{} // 종료 채널

	// 임배드 서버
	pbftv1.UnimplementedPBFTServiceServer
}

// peerConn 피어 노드와의 커넥션을 나타냄
type peerConn struct {
	id     string // 피어 노드 ID
	addr   string // 피어 주소
	conn   *grpc.ClientConn // gRPC 연결
	client pbftv1.PBFTServiceClient // gRPC 클라이언트
}

// GRPCTransportConfig gRPC 전송을 위한 설정을 나타냅니다.
type GRPCTransportConfig struct {
	NodeID  string // 노드 ID
	Address string // 리슨 주소
}

// NewGRPCTransport creates a new gRPC-based transport.
func NewGRPCTransport(nodeID, address string) (*GRPCTransport, error) {
	return &GRPCTransport{
		nodeID:  nodeID,
		address: address,
		peers:   make(map[string]*peerConn),
		done:    make(chan struct{}),
	}, nil
}

// Start starts the gRPC server.
func (t *GRPCTransport) Start() error {
	listener, err := net.Listen("tcp", t.address)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", t.address, err)
	}
	t.listener = listener

	t.server = grpc.NewServer(
		grpc.MaxRecvMsgSize(64 * 1024 * 1024), // 64MB
		grpc.MaxSendMsgSize(64 * 1024 * 1024),
	)
	pbftv1.RegisterPBFTServiceServer(t.server, t)

	t.mu.Lock()
	t.running = true
	t.mu.Unlock()

	go func() {
		if err := t.server.Serve(listener); err != nil {
			t.mu.RLock()
			running := t.running
			t.mu.RUnlock()
			if running {
				fmt.Printf("[GRPCTransport] Server error: %v\n", err)
			}
		}
	}()

	fmt.Printf("[GRPCTransport] Started on %s\n", t.address)
	return nil
}

// Stop stops the gRPC server and closes all connections.
func (t *GRPCTransport) Stop() {
	t.mu.Lock()
	t.running = false
	t.mu.Unlock()

	close(t.done)

	// Close all peer connections
	t.mu.Lock()
	for _, peer := range t.peers {
		if peer.conn != nil {
			peer.conn.Close()
		}
	}
	t.peers = make(map[string]*peerConn)
	t.mu.Unlock()

	// Gracefully stop the server
	if t.server != nil {
		t.server.GracefulStop()
	}

	fmt.Printf("[GRPCTransport] Stopped\n")
}

// AddPeer connects to a remote peer.
func (t *GRPCTransport) AddPeer(nodeID, address string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, err := grpc.DialContext(
		ctx,
		address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
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

	fmt.Printf("[GRPCTransport] Connected to peer %s at %s\n", nodeID, address)
	return nil
}

// RemovePeer disconnects from a peer.
func (t *GRPCTransport) RemovePeer(nodeID string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if peer, exists := t.peers[nodeID]; exists {
		if peer.conn != nil {
			peer.conn.Close()
		}
		delete(t.peers, nodeID)
		fmt.Printf("[GRPCTransport] Disconnected from peer %s\n", nodeID)
	}
}

// Broadcast sends a message to all connected peers.
func (t *GRPCTransport) Broadcast(msg *pbft.Message) error {
	t.mu.RLock()
	peers := make([]*peerConn, 0, len(t.peers))
	for _, peer := range t.peers {
		peers = append(peers, peer)
	}
	t.mu.RUnlock()

	protoMsg := messageToProto(msg)

	var wg sync.WaitGroup
	var errMu sync.Mutex
	var lastErr error

	for _, peer := range peers {
		wg.Add(1)
		go func(p *peerConn) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			_, err := p.client.BroadcastMessage(ctx, &pbftv1.BroadcastMessageRequest{
				Message: protoMsg,
			})
			if err != nil {
				errMu.Lock()
				lastErr = err
				errMu.Unlock()
				fmt.Printf("[GRPCTransport] Broadcast to %s failed: %v\n", p.id, err)
			}
		}(peer)
	}
	wg.Wait()

	return lastErr
}

// Send sends a message to a specific peer.
func (t *GRPCTransport) Send(nodeID string, msg *pbft.Message) error {
	t.mu.RLock()
	peer, exists := t.peers[nodeID]
	t.mu.RUnlock()

	if !exists {
		return fmt.Errorf("peer %s not found", nodeID)
	}

	protoMsg := messageToProto(msg)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := peer.client.SendMessage(ctx, &pbftv1.SendMessageRequest{
		TargetNodeId: nodeID,
		Message:      protoMsg,
	})
	return err
}

// SetMessageHandler sets the callback for incoming messages.
func (t *GRPCTransport) SetMessageHandler(handler func(*pbft.Message)) {
	t.msgHandler = handler
}

// SetStateProvider sets the state provider for sync operations.
func (t *GRPCTransport) SetStateProvider(provider StateProvider) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stateProvider = provider
}

// GetPeers returns the list of connected peer IDs.
func (t *GRPCTransport) GetPeers() []string {
	t.mu.RLock()
	defer t.mu.RUnlock()

	peers := make([]string, 0, len(t.peers))
	for id := range t.peers {
		peers = append(peers, id)
	}
	return peers
}

// PeerCount returns the number of connected peers.
func (t *GRPCTransport) PeerCount() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.peers)
}

// gRPC service implementations

// BroadcastMessage handles incoming broadcast messages from peers.
func (t *GRPCTransport) BroadcastMessage(ctx context.Context, req *pbftv1.BroadcastMessageRequest) (*pbftv1.BroadcastMessageResponse, error) {
	if t.msgHandler != nil && req.Message != nil {
		msg := protoToMessage(req.Message)
		t.msgHandler(msg)
	}
	return &pbftv1.BroadcastMessageResponse{Success: true}, nil
}

// SendMessage handles incoming direct messages from peers.
func (t *GRPCTransport) SendMessage(ctx context.Context, req *pbftv1.SendMessageRequest) (*pbftv1.SendMessageResponse, error) {
	if t.msgHandler != nil && req.Message != nil {
		msg := protoToMessage(req.Message)
		t.msgHandler(msg)
	}
	return &pbftv1.SendMessageResponse{Success: true}, nil
}

// MessageStream handles bidirectional message streaming.
func (t *GRPCTransport) MessageStream(stream pbftv1.PBFTService_MessageStreamServer) error {
	for {
		select {
		case <-t.done:
			return nil
		default:
		}

		protoMsg, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		if t.msgHandler != nil {
			msg := protoToMessage(protoMsg)
			t.msgHandler(msg)
		}
	}
}

// SyncState handles state synchronization requests.
// 새로운 노드가 네트워크에 참여할 때 기존 블록과 체크포인트를 동기화합니다.
func (t *GRPCTransport) SyncState(ctx context.Context, req *pbftv1.SyncStateRequest) (*pbftv1.SyncStateResponse, error) {
	t.mu.RLock()
	provider := t.stateProvider
	t.mu.RUnlock()

	// StateProvider가 설정되지 않은 경우 빈 응답 반환
	if provider == nil {
		return &pbftv1.SyncStateResponse{
			Blocks:      []*pbftv1.Block{},
			Checkpoints: []*pbftv1.Checkpoint{},
		}, nil
	}

	// 요청된 높이부터 블록들 조회
	blocks := provider.GetBlocksFromHeight(req.FromHeight)
	protoBlocks := make([]*pbftv1.Block, 0, len(blocks))
	for _, b := range blocks {
		protoBlocks = append(protoBlocks, typesBlockToProto(b))
	}

	// 체크포인트들 조회
	checkpoints := provider.GetCheckpoints()
	protoCheckpoints := make([]*pbftv1.Checkpoint, 0, len(checkpoints))
	for _, cp := range checkpoints {
		protoCheckpoints = append(protoCheckpoints, &pbftv1.Checkpoint{
			SequenceNum: cp.SequenceNum,
			Digest:      cp.Digest,
			NodeId:      cp.NodeID,
		})
	}

	return &pbftv1.SyncStateResponse{
		Blocks:      protoBlocks,
		Checkpoints: protoCheckpoints,
	}, nil
}

// GetCheckpoint handles checkpoint requests.
// 특정 시퀀스 번호의 체크포인트를 조회합니다.
func (t *GRPCTransport) GetCheckpoint(ctx context.Context, req *pbftv1.GetCheckpointRequest) (*pbftv1.GetCheckpointResponse, error) {
	t.mu.RLock()
	provider := t.stateProvider
	t.mu.RUnlock()

	// StateProvider가 설정되지 않은 경우 빈 응답 반환
	if provider == nil {
		return &pbftv1.GetCheckpointResponse{}, nil
	}

	// 특정 시퀀스 번호의 체크포인트 조회
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

// GetStatus returns the current node status.
func (t *GRPCTransport) GetStatus(ctx context.Context, req *pbftv1.GetStatusRequest) (*pbftv1.GetStatusResponse, error) {
	return &pbftv1.GetStatusResponse{
		NodeId:    t.nodeID,
		PeerCount: int32(t.PeerCount()),
	}, nil
}

// Helper functions for type conversion

// messageToProto converts a PBFT Message to protobuf format.
func messageToProto(msg *pbft.Message) *pbftv1.PBFTMessage {
	return &pbftv1.PBFTMessage{
		Type:        convertMessageType(msg.Type),
		View:        msg.View,
		SequenceNum: msg.SequenceNum,
		Digest:      msg.Digest,
		NodeId:      msg.NodeID,
		Timestamp:   timestamppb.New(msg.Timestamp),
		Signature:   msg.Signature,
		Payload:     msg.Payload,
	}
}

// protoToMessage converts a protobuf message to PBFT Message format.
func protoToMessage(proto *pbftv1.PBFTMessage) *pbft.Message {
	var ts time.Time
	if proto.Timestamp != nil {
		ts = proto.Timestamp.AsTime()
	}

	return &pbft.Message{
		Type:        convertProtoMessageType(proto.Type),
		View:        proto.View,
		SequenceNum: proto.SequenceNum,
		Digest:      proto.Digest,
		NodeID:      proto.NodeId,
		Timestamp:   ts,
		Signature:   proto.Signature,
		Payload:     proto.Payload,
	}
}

// convertMessageType converts PBFT MessageType to protobuf MessageType.
func convertMessageType(mt pbft.MessageType) pbftv1.MessageType {
	switch mt {
	case pbft.PrePrepare:
		return pbftv1.MessageType_MESSAGE_TYPE_PRE_PREPARE
	case pbft.Prepare:
		return pbftv1.MessageType_MESSAGE_TYPE_PREPARE
	case pbft.Commit:
		return pbftv1.MessageType_MESSAGE_TYPE_COMMIT
	case pbft.ViewChange:
		return pbftv1.MessageType_MESSAGE_TYPE_VIEW_CHANGE
	case pbft.NewView:
		return pbftv1.MessageType_MESSAGE_TYPE_NEW_VIEW
	case pbft.CheckpointMsgType:
		return pbftv1.MessageType_MESSAGE_TYPE_CHECKPOINT
	default:
		return pbftv1.MessageType_MESSAGE_TYPE_UNSPECIFIED
	}
}

// convertProtoMessageType converts protobuf MessageType to PBFT MessageType.
func convertProtoMessageType(mt pbftv1.MessageType) pbft.MessageType {
	switch mt {
	case pbftv1.MessageType_MESSAGE_TYPE_PRE_PREPARE:
		return pbft.PrePrepare
	case pbftv1.MessageType_MESSAGE_TYPE_PREPARE:
		return pbft.Prepare
	case pbftv1.MessageType_MESSAGE_TYPE_COMMIT:
		return pbft.Commit
	case pbftv1.MessageType_MESSAGE_TYPE_VIEW_CHANGE:
		return pbft.ViewChange
	case pbftv1.MessageType_MESSAGE_TYPE_NEW_VIEW:
		return pbft.NewView
	case pbftv1.MessageType_MESSAGE_TYPE_CHECKPOINT:
		return pbft.CheckpointMsgType
	default:
		return pbft.Request // default fallback
	}
}

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

// Ensure GRPCTransport implements TransportInterface
var _ TransportInterface = (*GRPCTransport)(nil)

// typesBlockToProto converts a types.Block to protobuf Block format.
func typesBlockToProto(b *types.Block) *pbftv1.Block {
	if b == nil {
		return nil
	}

	// 트랜잭션들을 바이트 슬라이스로 변환
	txs := make([][]byte, 0, len(b.Transactions))
	for _, tx := range b.Transactions {
		txs = append(txs, tx.Data)
	}

	return &pbftv1.Block{
		Header: &pbftv1.BlockHeader{
			Height:    b.Header.Height,
			Timestamp: timestamppb.New(b.Header.Timestamp),
			PrevHash:  b.Header.PrevHash,
			TxHash:    b.Header.TxRoot,
			StateHash: b.Header.StateRoot,
			Proposer:  b.Header.ProposerID,
			View:      b.Header.View,
		},
		Txs:  txs,
		Hash: b.Hash,
	}
}
