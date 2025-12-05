// Package pbft implements the Practical Byzantine Fault Tolerance consensus algorithm.
package pbft

import (
	"sync"

	"github.com/ahwlsqja/pbft-cosmos/types"
)

// Phase represents the current phase of PBFT consensus.
type Phase int

const (
	// Idle - waiting for new requests.
	Idle Phase = iota
	// PrePrepared - received pre-prepare, waiting for prepares.
	PrePrepared
	// Prepared - received 2f+1 prepares, waiting for commits.
	Prepared
	// Committed - received 2f+1 commits, ready to execute.
	Committed
)

// String returns the string representation of Phase.
func (p Phase) String() string {
	switch p {
	case Idle:
		return "IDLE"
	case PrePrepared:
		return "PRE-PREPARED"
	case Prepared:
		return "PREPARED"
	case Committed:
		return "COMMITTED"
	default:
		return "UNKNOWN"
	}
}

// State represents the consensus state for a specific sequence number.
type State struct {
	mu sync.RWMutex

	// Current view number
	View uint64

	// Current sequence number
	SequenceNum uint64

	// Current phase
	Phase Phase

	// The block being processed
	Block *types.Block

	// Pre-prepare message for this sequence
	PrePrepareMsg *PrePrepareMsg

	// Prepare messages received (nodeID -> PrepareMsg)
	PrepareMsgs map[string]*PrepareMsg

	// Commit messages received (nodeID -> CommitMsg)
	CommitMsgs map[string]*CommitMsg

	// Whether this state has been executed
	Executed bool
}

// NewState creates a new consensus state.
func NewState(view, seqNum uint64) *State {
	return &State{
		View:        view,
		SequenceNum: seqNum,
		Phase:       Idle,
		PrepareMsgs: make(map[string]*PrepareMsg),
		CommitMsgs:  make(map[string]*CommitMsg),
		Executed:    false,
	}
}

// SetPrePrepare sets the pre-prepare message and transitions to PrePrepared phase.
func (s *State) SetPrePrepare(msg *PrePrepareMsg, block *types.Block) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.PrePrepareMsg = msg
	s.Block = block
	s.Phase = PrePrepared
}

// AddPrepare adds a prepare message.
func (s *State) AddPrepare(msg *PrepareMsg) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.PrepareMsgs[msg.NodeID] = msg
}

// AddCommit adds a commit message.
func (s *State) AddCommit(msg *CommitMsg) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.CommitMsgs[msg.NodeID] = msg
}

// PrepareCount returns the number of prepare messages received.
func (s *State) PrepareCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return len(s.PrepareMsgs)
}

// CommitCount returns the number of commit messages received.
func (s *State) CommitCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return len(s.CommitMsgs)
}

// IsPrepared checks if the state is prepared (received 2f+1 prepares).
func (s *State) IsPrepared(quorum int) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return len(s.PrepareMsgs) >= quorum && s.Phase >= PrePrepared
}

// IsCommitted checks if the state is committed (received 2f+1 commits).
func (s *State) IsCommitted(quorum int) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return len(s.CommitMsgs) >= quorum && s.Phase >= Prepared
}

// TransitionToPrepared transitions to Prepared phase.
func (s *State) TransitionToPrepared() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.Phase == PrePrepared {
		s.Phase = Prepared
	}
}

// TransitionToCommitted transitions to Committed phase.
func (s *State) TransitionToCommitted() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.Phase == Prepared {
		s.Phase = Committed
	}
}

// MarkExecuted marks the state as executed.
func (s *State) MarkExecuted() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.Executed = true
}

// GetPhase returns the current phase.
func (s *State) GetPhase() Phase {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.Phase
}

// StateLog는 다중 시퀸스 번호를 위한 시퀸스 상태를 유지한다.
type StateLog struct {
	// 읽기 락
	mu     sync.RWMutex
	// 상태는 주소 배열임
	states map[uint64]*State

	// 최소한의 워터마크 [1, ..... ,19] 이라고 치면 1을 말하는듯 컨텍스트 윈도우 시작 
	LowWaterMark uint64

	// High water mark - 수용할 수 있는 최대한의 시퀸스 넘버
	HighWaterMark uint64

	// 상태 관리를 위한 윈도우 사이즈
	WindowSize uint64
}

// NewStateLog creates a new state log.
func NewStateLog(windowSize uint64) *StateLog {
	return &StateLog{
		states:        make(map[uint64]*State),
		LowWaterMark:  0,
		HighWaterMark: windowSize,
		WindowSize:    windowSize,
	}
}

// GetState returns the state for a sequence number, creating it if necessary.
func (sl *StateLog) GetState(view, seqNum uint64) *State {
	sl.mu.Lock()
	defer sl.mu.Unlock()

	if state, exists := sl.states[seqNum]; exists {
		return state
	}

	state := NewState(view, seqNum)
	sl.states[seqNum] = state
	return state
}

// GetExistingState returns the state for a sequence number if it exists.
func (sl *StateLog) GetExistingState(seqNum uint64) *State {
	sl.mu.RLock()
	defer sl.mu.RUnlock()

	return sl.states[seqNum]
}

// IsInWindow checks 시퀸스 번호가 수용가능한 윈도우에 있는지 확인하는 함수
func (sl *StateLog) IsInWindow(seqNum uint64) bool {
	// 읽기 락걸고
	sl.mu.RLock()
	// 끝날때 풀음
	defer sl.mu.RUnlock()

	// 만약 시퀸스 넘버가 수용가능 범위 내에 있는지 boolean으로 return함
	return seqNum > sl.LowWaterMark && seqNum <= sl.HighWaterMark
}

// AdvanceWatermarks advances the water marks after a checkpoint.
func (sl *StateLog) AdvanceWatermarks(checkpoint uint64) {
	sl.mu.Lock()
	defer sl.mu.Unlock()

	if checkpoint > sl.LowWaterMark {
		sl.LowWaterMark = checkpoint
		sl.HighWaterMark = checkpoint + sl.WindowSize

		// Garbage collect old states
		for seqNum := range sl.states {
			if seqNum <= sl.LowWaterMark {
				delete(sl.states, seqNum)
			}
		}
	}
}

// GetPendingStates returns states that are committed but not yet executed.
func (sl *StateLog) GetPendingStates(quorum int) []*State {
	sl.mu.RLock()
	defer sl.mu.RUnlock()

	var pending []*State
	for _, state := range sl.states {
		if state.IsCommitted(quorum) && !state.Executed {
			pending = append(pending, state)
		}
	}
	return pending
}
