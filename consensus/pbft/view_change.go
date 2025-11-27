// Package pbft implements the Practical Byzantine Fault Tolerance consensus algorithm.
package pbft

import (
	"encoding/json"
	"sync"
	"time"
)

// ViewChangeManager manages the view change protocol.
type ViewChangeManager struct {
	mu sync.RWMutex

	// Current view
	currentView uint64

	// View change messages received (view -> nodeID -> message)
	viewChangeMsgs map[uint64]map[string]*ViewChangeMsg

	// New view messages received
	newViewMsgs map[uint64]*NewViewMsg

	// Whether view change is in progress
	inProgress bool

	// Quorum size
	quorumSize int

	// Node ID
	nodeID string

	// Callback for broadcasting messages
	broadcastFunc func(*Message)

	// Callback when view change completes
	onViewChangeComplete func(newView uint64)
}

// NewViewChangeManager creates a new view change manager.
func NewViewChangeManager(nodeID string, quorumSize int) *ViewChangeManager {
	return &ViewChangeManager{
		currentView:    0,
		viewChangeMsgs: make(map[uint64]map[string]*ViewChangeMsg),
		newViewMsgs:    make(map[uint64]*NewViewMsg),
		inProgress:     false,
		quorumSize:     quorumSize,
		nodeID:         nodeID,
	}
}

// SetBroadcastFunc sets the function used to broadcast messages.
func (vcm *ViewChangeManager) SetBroadcastFunc(f func(*Message)) {
	vcm.broadcastFunc = f
}

// SetOnViewChangeComplete sets the callback for view change completion.
func (vcm *ViewChangeManager) SetOnViewChangeComplete(f func(newView uint64)) {
	vcm.onViewChangeComplete = f
}

// StartViewChange initiates a view change to the specified view.
func (vcm *ViewChangeManager) StartViewChange(newView uint64, lastSeqNum uint64, checkpoints []Checkpoint, preparedSet []PreparedCert) {
	vcm.mu.Lock()
	defer vcm.mu.Unlock()

	if newView <= vcm.currentView {
		return
	}

	vcm.inProgress = true

	// Create view change message
	viewChangeMsg := &ViewChangeMsg{
		NewView:     newView,
		LastSeqNum:  lastSeqNum,
		Checkpoints: checkpoints,
		PreparedSet: preparedSet,
		NodeID:      vcm.nodeID,
	}

	// Store our own view change message
	if vcm.viewChangeMsgs[newView] == nil {
		vcm.viewChangeMsgs[newView] = make(map[string]*ViewChangeMsg)
	}
	vcm.viewChangeMsgs[newView][vcm.nodeID] = viewChangeMsg

	// Broadcast view change
	if vcm.broadcastFunc != nil {
		payload, _ := json.Marshal(viewChangeMsg)
		msg := NewMessage(ViewChange, newView, lastSeqNum, nil, vcm.nodeID)
		msg.Payload = payload
		vcm.broadcastFunc(msg)
	}
}

// HandleViewChange processes a received view change message.
func (vcm *ViewChangeManager) HandleViewChange(msg *ViewChangeMsg) bool {
	vcm.mu.Lock()
	defer vcm.mu.Unlock()

	// Initialize map for this view if needed
	if vcm.viewChangeMsgs[msg.NewView] == nil {
		vcm.viewChangeMsgs[msg.NewView] = make(map[string]*ViewChangeMsg)
	}

	// Store the message
	vcm.viewChangeMsgs[msg.NewView][msg.NodeID] = msg

	// Check if we have enough view change messages (2f+1)
	if len(vcm.viewChangeMsgs[msg.NewView]) >= vcm.quorumSize {
		return true
	}

	return false
}

// CreateNewViewMsg creates a new view message (called by the new primary).
func (vcm *ViewChangeManager) CreateNewViewMsg(newView uint64, validatorCount int) *NewViewMsg {
	vcm.mu.RLock()
	defer vcm.mu.RUnlock()

	viewChangeMsgs := vcm.viewChangeMsgs[newView]
	if len(viewChangeMsgs) < vcm.quorumSize {
		return nil
	}

	// Collect view change messages
	var vcMsgs []ViewChangeMsg
	for _, msg := range viewChangeMsgs {
		vcMsgs = append(vcMsgs, *msg)
	}

	// Compute O (set of pre-prepare messages for the new view)
	prePrepares := vcm.computePrePrepareSet(vcMsgs)

	newViewMsg := &NewViewMsg{
		View:           newView,
		ViewChangeMsgs: vcMsgs,
		PrePrepareMsgs: prePrepares,
		NewPrimaryID:   vcm.nodeID,
	}

	return newViewMsg
}

// computePrePrepareSet computes the set of pre-prepare messages for the new view.
func (vcm *ViewChangeManager) computePrePrepareSet(viewChangeMsgs []ViewChangeMsg) []PrePrepareMsg {
	// Find the maximum stable checkpoint sequence number
	var maxCheckpoint uint64
	for _, vcMsg := range viewChangeMsgs {
		for _, cp := range vcMsg.Checkpoints {
			if cp.SequenceNum > maxCheckpoint {
				maxCheckpoint = cp.SequenceNum
			}
		}
	}

	// Find the maximum sequence number in prepared certificates
	var maxPrepared uint64
	preparedBlocks := make(map[uint64]*PrePrepareMsg)

	for _, vcMsg := range viewChangeMsgs {
		for _, pc := range vcMsg.PreparedSet {
			if pc.PrePrepare.SequenceNum > maxPrepared {
				maxPrepared = pc.PrePrepare.SequenceNum
			}
			// Keep track of prepared blocks
			if _, exists := preparedBlocks[pc.PrePrepare.SequenceNum]; !exists {
				pp := pc.PrePrepare
				preparedBlocks[pc.PrePrepare.SequenceNum] = &pp
			}
		}
	}

	// Create pre-prepare messages for all sequence numbers between
	// maxCheckpoint and maxPrepared
	var prePrepares []PrePrepareMsg
	for seqNum := maxCheckpoint + 1; seqNum <= maxPrepared; seqNum++ {
		if pp, exists := preparedBlocks[seqNum]; exists {
			prePrepares = append(prePrepares, *pp)
		}
		// For gaps, we would create null requests (not implemented here)
	}

	return prePrepares
}

// HandleNewView processes a received new view message.
func (vcm *ViewChangeManager) HandleNewView(msg *NewViewMsg) bool {
	vcm.mu.Lock()
	defer vcm.mu.Unlock()

	// Verify the new view message
	if !vcm.verifyNewViewMsg(msg) {
		return false
	}

	// Store the new view message
	vcm.newViewMsgs[msg.View] = msg

	// Update current view
	vcm.currentView = msg.View
	vcm.inProgress = false

	// Clean up old view change messages
	for v := range vcm.viewChangeMsgs {
		if v <= msg.View {
			delete(vcm.viewChangeMsgs, v)
		}
	}

	// Notify completion
	if vcm.onViewChangeComplete != nil {
		vcm.onViewChangeComplete(msg.View)
	}

	return true
}

// verifyNewViewMsg verifies a new view message.
func (vcm *ViewChangeManager) verifyNewViewMsg(msg *NewViewMsg) bool {
	// Verify we have 2f+1 view change messages
	if len(msg.ViewChangeMsgs) < vcm.quorumSize {
		return false
	}

	// Verify all view change messages are for the same view
	for _, vcMsg := range msg.ViewChangeMsgs {
		if vcMsg.NewView != msg.View {
			return false
		}
	}

	// TODO: Verify pre-prepare set is correctly computed

	return true
}

// IsInProgress returns whether a view change is in progress.
func (vcm *ViewChangeManager) IsInProgress() bool {
	vcm.mu.RLock()
	defer vcm.mu.RUnlock()
	return vcm.inProgress
}

// GetCurrentView returns the current view.
func (vcm *ViewChangeManager) GetCurrentView() uint64 {
	vcm.mu.RLock()
	defer vcm.mu.RUnlock()
	return vcm.currentView
}

// ViewChangeTimer manages the view change timeout.
type ViewChangeTimer struct {
	mu sync.Mutex

	timeout time.Duration
	timer   *time.Timer
	onTimeout func()
}

// NewViewChangeTimer creates a new view change timer.
func NewViewChangeTimer(timeout time.Duration, onTimeout func()) *ViewChangeTimer {
	return &ViewChangeTimer{
		timeout:   timeout,
		onTimeout: onTimeout,
	}
}

// Start starts or resets the timer.
func (vct *ViewChangeTimer) Start() {
	vct.mu.Lock()
	defer vct.mu.Unlock()

	if vct.timer != nil {
		vct.timer.Stop()
	}
	vct.timer = time.AfterFunc(vct.timeout, vct.onTimeout)
}

// Stop stops the timer.
func (vct *ViewChangeTimer) Stop() {
	vct.mu.Lock()
	defer vct.mu.Unlock()

	if vct.timer != nil {
		vct.timer.Stop()
		vct.timer = nil
	}
}

// Reset resets the timer with a new timeout value.
func (vct *ViewChangeTimer) Reset(timeout time.Duration) {
	vct.mu.Lock()
	defer vct.mu.Unlock()

	vct.timeout = timeout
	if vct.timer != nil {
		vct.timer.Stop()
	}
	vct.timer = time.AfterFunc(vct.timeout, vct.onTimeout)
}
