// Package network provides P2P networking for the PBFT consensus engine.
package network

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"

	"github.com/ahwlsqja/pbft-cosmos/consensus/pbft"
)

// Peer represents a remote peer in the network.
type Peer struct {
	ID      string
	Address string
	conn    net.Conn
	mu      sync.Mutex
}

// Transport handles P2P communication between PBFT nodes.
type Transport struct {
	mu sync.RWMutex

	// Local node information
	nodeID  string
	address string

	// Network listener
	listener net.Listener

	// Connected peers (nodeID -> Peer)
	peers map[string]*Peer

	// Message handler callback
	messageHandler func(*pbft.Message)

	// Logger
	logger *log.Logger

	// Running state
	running bool
	done    chan struct{}
}

// TransportConfig holds configuration for the transport layer.
type TransportConfig struct {
	NodeID  string
	Address string
	Peers   []PeerConfig
}

// PeerConfig holds configuration for a peer.
type PeerConfig struct {
	ID      string
	Address string
}

// NewTransport creates a new P2P transport.
func NewTransport(config *TransportConfig) *Transport {
	return &Transport{
		nodeID:  config.NodeID,
		address: config.Address,
		peers:   make(map[string]*Peer),
		logger:  log.Default(),
		done:    make(chan struct{}),
	}
}

// Start starts the transport layer.
func (t *Transport) Start() error {
	listener, err := net.Listen("tcp", t.address)
	if err != nil {
		return fmt.Errorf("failed to start listener: %w", err)
	}

	t.listener = listener
	t.running = true

	t.logger.Printf("[Transport] Listening on %s", t.address)

	// Start accepting connections
	go t.acceptConnections()

	return nil
}

// Stop stops the transport layer.
func (t *Transport) Stop() error {
	t.mu.Lock()
	t.running = false
	t.mu.Unlock()

	close(t.done)

	// Close listener
	if t.listener != nil {
		t.listener.Close()
	}

	// Close all peer connections
	for _, peer := range t.peers {
		peer.mu.Lock()
		if peer.conn != nil {
			peer.conn.Close()
		}
		peer.mu.Unlock()
	}

	return nil
}

// acceptConnections accepts incoming connections.
func (t *Transport) acceptConnections() {
	for {
		select {
		case <-t.done:
			return
		default:
		}

		conn, err := t.listener.Accept()
		if err != nil {
			t.mu.RLock()
			running := t.running
			t.mu.RUnlock()
			if !running {
				return
			}
			t.logger.Printf("[Transport] Accept error: %v", err)
			continue
		}

		go t.handleConnection(conn)
	}
}

// handleConnection handles an incoming connection.
func (t *Transport) handleConnection(conn net.Conn) {
	defer conn.Close()

	// Read handshake
	decoder := json.NewDecoder(conn)
	var handshake struct {
		NodeID string `json:"node_id"`
	}
	if err := decoder.Decode(&handshake); err != nil {
		t.logger.Printf("[Transport] Handshake failed: %v", err)
		return
	}

	// Register peer
	t.mu.Lock()
	peer := &Peer{
		ID:      handshake.NodeID,
		Address: conn.RemoteAddr().String(),
		conn:    conn,
	}
	t.peers[handshake.NodeID] = peer
	t.mu.Unlock()

	t.logger.Printf("[Transport] Connected to peer %s", handshake.NodeID)

	// Read messages
	for {
		select {
		case <-t.done:
			return
		default:
		}

		var msg pbft.Message
		if err := decoder.Decode(&msg); err != nil {
			if err == io.EOF {
				break
			}
			t.logger.Printf("[Transport] Read error from %s: %v", handshake.NodeID, err)
			break
		}

		// Handle message
		if t.messageHandler != nil {
			t.messageHandler(&msg)
		}
	}

	// Remove peer on disconnect
	t.mu.Lock()
	delete(t.peers, handshake.NodeID)
	t.mu.Unlock()

	t.logger.Printf("[Transport] Disconnected from peer %s", handshake.NodeID)
}

// Connect connects to a remote peer.
func (t *Transport) Connect(peerID, address string) error {
	conn, err := net.DialTimeout("tcp", address, 10*time.Second)
	if err != nil {
		return fmt.Errorf("failed to connect to %s: %w", address, err)
	}

	// Send handshake
	encoder := json.NewEncoder(conn)
	handshake := struct {
		NodeID string `json:"node_id"`
	}{
		NodeID: t.nodeID,
	}
	if err := encoder.Encode(&handshake); err != nil {
		conn.Close()
		return fmt.Errorf("handshake failed: %w", err)
	}

	// Register peer
	t.mu.Lock()
	peer := &Peer{
		ID:      peerID,
		Address: address,
		conn:    conn,
	}
	t.peers[peerID] = peer
	t.mu.Unlock()

	t.logger.Printf("[Transport] Connected to peer %s at %s", peerID, address)

	// Start reading from peer
	go t.handleConnection(conn)

	return nil
}

// Broadcast 모든 연결된 피어들에게 메시지를 전송함
func (t *Transport) Broadcast(msg *pbft.Message) error {
	t.mu.RLock()
	peers := make([]*Peer, 0, len(t.peers))
	for _, peer := range t.peers {
		peers = append(peers, peer)
	}
	t.mu.RUnlock()

	var lastErr error
	for _, peer := range peers {
		if err := t.sendToPeer(peer, msg); err != nil {
			lastErr = err
			t.logger.Printf("[Transport] Failed to send to %s: %v", peer.ID, err)
		}
	}

	return lastErr
}

// Send sends a message to a specific peer.
func (t *Transport) Send(nodeID string, msg *pbft.Message) error {
	t.mu.RLock()
	peer, exists := t.peers[nodeID]
	t.mu.RUnlock()

	if !exists {
		return fmt.Errorf("peer %s not found", nodeID)
	}

	return t.sendToPeer(peer, msg)
}

// sendToPeer 한 특정한 노드에게 메시지를 TCP 소켓을 통해 전송함
//
// [네트워크 전송 원리 - 로우 레벨 설명]
//
// 1. peer.conn은 net.Conn 타입 (TCP 소켓 연결)
//    - net.Dial() 또는 listener.Accept()로 생성된 실제 TCP 연결
//    - 운영체제 커널의 소켓 파일 디스크립터(fd)를 래핑한 객체
//
// 2. json.NewEncoder(peer.conn) 동작 원리:
//    - peer.conn은 io.Writer 인터페이스를 구현함 (Write([]byte) 메서드 보유)
//    - Encoder는 내부적으로 이 Writer를 저장하고, Encode() 호출 시 여기에 씀
//
// 3. encoder.Encode(msg) 실행 시 내부 동작:
//    a) msg 구조체를 JSON 바이트로 직렬화 (예: {"type":1,"view":0,...})
//    b) 직렬화된 바이트를 peer.conn.Write(bytes)로 전달
//    c) net.Conn.Write()는 내부적으로 syscall.Write() 호출
//    d) 커널이 TCP 송신 버퍼에 데이터를 복사
//    e) TCP/IP 스택이 패킷을 만들어 네트워크 인터페이스로 전송
//    f) 상대방 피어의 TCP 수신 버퍼에 도착
//
// [데이터 흐름]
//
//   Go 애플리케이션                    운영체제 커널                   네트워크
//   ┌─────────────┐                 ┌─────────────┐              ┌─────────┐
//   │ msg 구조체   │                 │ TCP 송신버퍼 │              │  이더넷  │
//   │     ↓       │                 │     ↓       │              │   카드   │
//   │ JSON 직렬화  │ ──Write()───▶  │  TCP 세그먼트 │ ──────────▶ │    ↓    │
//   │     ↓       │   (syscall)    │     생성     │              │ 상대 피어 │
//   │ []byte 데이터│                 │     ↓       │              │   수신   │
//   └─────────────┘                 │  IP 패킷화   │              └─────────┘
//                                   └─────────────┘
//
// [왜 Encode()가 전송인가?]
//
// json.Encoder는 "어디에 쓸지"를 생성자에서 받음
// - json.NewEncoder(os.Stdout)  → 표준출력에 씀
// - json.NewEncoder(file)       → 파일에 씀
// - json.NewEncoder(peer.conn)  → TCP 소켓에 씀 = 네트워크 전송!
//
// 즉, Encode()는 "직렬화 + Write()"를 한 번에 수행하는 함수
// peer.conn에 Write하면 TCP 스택을 타고 상대방에게 전송됨
func (t *Transport) sendToPeer(peer *Peer, msg *pbft.Message) error {
	// 동시에 같은 연결에 쓰는 것을 방지하기 위한 뮤텍스 잠금
	// TCP는 바이트 스트림이므로, 동시 쓰기 시 메시지가 섞일 수 있음
	peer.mu.Lock()
	defer peer.mu.Unlock()

	// 연결이 끊어졌는지 확인
	if peer.conn == nil {
		return fmt.Errorf("no connection to peer %s", peer.ID)
	}

	// peer.conn(TCP 소켓)에 JSON을 쓰는 Encoder 생성
	// 이 시점에서는 아직 아무것도 전송되지 않음
	encoder := json.NewEncoder(peer.conn)

	// Encode() 호출 = JSON 직렬화 + TCP 소켓에 Write = 네트워크 전송
	// 이 한 줄이 실행되면 바이트가 커널 TCP 버퍼로 들어가고,
	// TCP/IP 스택이 패킷을 만들어 상대방에게 실제로 전송함
	return encoder.Encode(msg)
}

// SetMessageHandler sets the callback for incoming messages.
func (t *Transport) SetMessageHandler(handler func(*pbft.Message)) {
	t.messageHandler = handler
}

// GetPeers returns the list of connected peer IDs.
func (t *Transport) GetPeers() []string {
	t.mu.RLock()
	defer t.mu.RUnlock()

	peers := make([]string, 0, len(t.peers))
	for id := range t.peers {
		peers = append(peers, id)
	}
	return peers
}

// PeerCount returns the number of connected peers.
func (t *Transport) PeerCount() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.peers)
}

// MockTransport is a mock transport for testing.
type MockTransport struct {
	mu             sync.RWMutex
	nodeID         string
	messageHandler func(*pbft.Message)
	sentMessages   []*pbft.Message
	peers          []string
}

// NewMockTransport creates a new mock transport.
func NewMockTransport(nodeID string, peers []string) *MockTransport {
	return &MockTransport{
		nodeID:       nodeID,
		sentMessages: make([]*pbft.Message, 0),
		peers:        peers,
	}
}

// Broadcast records the message for testing.
func (mt *MockTransport) Broadcast(msg *pbft.Message) error {
	mt.mu.Lock()
	defer mt.mu.Unlock()
	mt.sentMessages = append(mt.sentMessages, msg)
	return nil
}

// Send records the message for testing.
func (mt *MockTransport) Send(nodeID string, msg *pbft.Message) error {
	mt.mu.Lock()
	defer mt.mu.Unlock()
	mt.sentMessages = append(mt.sentMessages, msg)
	return nil
}

// SetMessageHandler sets the message handler.
func (mt *MockTransport) SetMessageHandler(handler func(*pbft.Message)) {
	mt.messageHandler = handler
}

// SimulateReceive simulates receiving a message.
func (mt *MockTransport) SimulateReceive(msg *pbft.Message) {
	if mt.messageHandler != nil {
		mt.messageHandler(msg)
	}
}

// GetSentMessages returns all sent messages.
func (mt *MockTransport) GetSentMessages() []*pbft.Message {
	mt.mu.RLock()
	defer mt.mu.RUnlock()
	return mt.sentMessages
}

// ClearSentMessages clears the sent messages.
func (mt *MockTransport) ClearSentMessages() {
	mt.mu.Lock()
	defer mt.mu.Unlock()
	mt.sentMessages = make([]*pbft.Message, 0)
}
