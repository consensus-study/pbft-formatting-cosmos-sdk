// Package pbft implements the Practical Byzantine Fault Tolerance consensus algorithm.
package pbft

import (
	"crypto/sha256"
	"encoding/json"
	"time"

	"github.com/ahwlsqja/pbft-cosmos/types"
)

// MessageType represents the type of PBFT message.
type MessageType int

const (
	// PrePrepare is sent by the primary to initiate consensus.
	PrePrepare MessageType = iota
	// Prepare is sent by replicas after receiving PrePrepare.
	Prepare
	// Commit is sent after receiving 2f+1 Prepare messages.
	Commit
	// ViewChange is sent to trigger a view change.
	ViewChange
	// NewView is sent by the new primary after view change.
	NewView
	// CheckpointMsgType is sent for garbage collection.
	CheckpointMsgType
	// Request is a client request.
	Request
	// Reply is a response to a client request.
	Reply
)

// String returns the string representation of MessageType.
func (mt MessageType) String() string {
	switch mt {
	case PrePrepare:
		return "PRE-PREPARE"
	case Prepare:
		return "PREPARE"
	case Commit:
		return "COMMIT"
	case ViewChange:
		return "VIEW-CHANGE"
	case NewView:
		return "NEW-VIEW"
	case CheckpointMsgType:
		return "CHECKPOINT"
	case Request:
		return "REQUEST"
	case Reply:
		return "REPLY"
	default:
		return "UNKNOWN"
	}
}

// Message represents a PBFT consensus message.
type Message struct {
	Type        MessageType `json:"type"`
	View        uint64      `json:"view"`
	SequenceNum uint64      `json:"sequence_num"`
	Digest      []byte      `json:"digest"`
	NodeID      string      `json:"node_id"`
	Timestamp   time.Time   `json:"timestamp"`
	Signature   []byte      `json:"signature"`
	Payload     []byte      `json:"payload,omitempty"`
}

// PrePrepareMsg contains the pre-prepare message data.
type PrePrepareMsg struct {
	View        uint64       `json:"view"`
	SequenceNum uint64       `json:"sequence_num"`
	Digest      []byte       `json:"digest"`
	Block       *types.Block `json:"block"`
	PrimaryID   string       `json:"primary_id"`
}

// PrepareMsg contains the prepare message data.
type PrepareMsg struct {
	View        uint64 `json:"view"`
	SequenceNum uint64 `json:"sequence_num"`
	Digest      []byte `json:"digest"`
	NodeID      string `json:"node_id"`
}

// CommitMsg contains the commit message data.
type CommitMsg struct {
	View        uint64 `json:"view"`
	SequenceNum uint64 `json:"sequence_num"`
	Digest      []byte `json:"digest"`
	NodeID      string `json:"node_id"`
}

// ViewChangeMsg contains the view change message data.
type ViewChangeMsg struct {
	NewView     uint64        `json:"new_view"`
	LastSeqNum  uint64        `json:"last_seq_num"`
	Checkpoints []Checkpoint  `json:"checkpoints"`
	PreparedSet []PreparedCert `json:"prepared_set"`
	NodeID      string        `json:"node_id"`
}

// NewViewMsg contains the new view message data.
type NewViewMsg struct {
	View           uint64          `json:"view"`
	ViewChangeMsgs []ViewChangeMsg `json:"view_change_msgs"`
	PrePrepareMsgs []PrePrepareMsg `json:"pre_prepare_msgs"`
	NewPrimaryID   string          `json:"new_primary_id"`
}

// Checkpoint represents a stable checkpoint.
type Checkpoint struct {
	SequenceNum uint64 `json:"sequence_num"`
	Digest      []byte `json:"digest"`
	NodeID      string `json:"node_id"`
}

// PreparedCert represents a prepared certificate.
type PreparedCert struct {
	PrePrepare PrePrepareMsg `json:"pre_prepare"`
	Prepares   []PrepareMsg  `json:"prepares"`
}

// RequestMsg contains a client request.
type RequestMsg struct {
	Operation []byte    `json:"operation"`
	Timestamp time.Time `json:"timestamp"`
	ClientID  string    `json:"client_id"`
}

// ReplyMsg contains a reply to a client request.
type ReplyMsg struct {
	View      uint64 `json:"view"`
	Timestamp time.Time `json:"timestamp"`
	ClientID  string    `json:"client_id"`
	NodeID    string    `json:"node_id"`
	Result    []byte    `json:"result"`
}

// NewMessage 새로운 PBFT 메시지를 생성함.
func NewMessage(msgType MessageType, view, seqNum uint64, digest []byte, nodeID string) *Message {
	return &Message{
		Type:        msgType,
		View:        view,
		SequenceNum: seqNum,
		Digest:      digest,
		NodeID:      nodeID,
		Timestamp:   time.Now(),
	}
}

// NewPrePrepareMsg creates a new PrePrepare message.
func NewPrePrepareMsg(view, seqNum uint64, block *types.Block, primaryID string) *PrePrepareMsg {
	return &PrePrepareMsg{
		View:        view,
		SequenceNum: seqNum,
		Digest:      block.Hash,
		Block:       block,
		PrimaryID:   primaryID,
	}
}

// NewPrepareMsg creates a new Prepare message.
func NewPrepareMsg(view, seqNum uint64, digest []byte, nodeID string) *PrepareMsg {
	return &PrepareMsg{
		View:        view,
		SequenceNum: seqNum,
		Digest:      digest,
		NodeID:      nodeID,
	}
}

// NewCommitMsg 새로운 커밋 메시지를 생성함
func NewCommitMsg(view, seqNum uint64, digest []byte, nodeID string) *CommitMsg {
	return &CommitMsg{
		View:        view, // 현재 뷰
		SequenceNum: seqNum, // 블록 높이
		Digest:      digest, // 블록 해시
		NodeID:      nodeID, // 노드 ID 발신자
	}
}

// Hash computes the hash of the message.
func (m *Message) Hash() []byte {
	data, _ := json.Marshal(m)
	hash := sha256.Sum256(data)
	return hash[:]
}

// Encode serializes the message to JSON.
func (m *Message) Encode() ([]byte, error) {
	return json.Marshal(m)
}

// DecodeMessage deserializes a message from JSON.
func DecodeMessage(data []byte) (*Message, error) {
	var msg Message
	err := json.Unmarshal(data, &msg)
	if err != nil {
		return nil, err
	}
	return &msg, nil
}
