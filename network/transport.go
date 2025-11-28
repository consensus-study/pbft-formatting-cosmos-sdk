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

// Broadcast sends a message to all connected peers.
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

// sendToPeer sends a message to a specific peer.
func (t *Transport) sendToPeer(peer *Peer, msg *pbft.Message) error {
	peer.mu.Lock()
	defer peer.mu.Unlock()

	if peer.conn == nil {
		return fmt.Errorf("no connection to peer %s", peer.ID)
	}

	encoder := json.NewEncoder(peer.conn)
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
