// Package unit provides unit tests for the PBFT consensus engine.
package unit

import (
	"testing"
	"time"

	"github.com/ahwlsqja/pbft-cosmos/abci"
	"github.com/ahwlsqja/pbft-cosmos/consensus/pbft"
	"github.com/ahwlsqja/pbft-cosmos/network"
	"github.com/ahwlsqja/pbft-cosmos/types"
)

func TestNewState(t *testing.T) {
	state := pbft.NewState(1, 100)

	if state.View != 1 {
		t.Errorf("expected view 1, got %d", state.View)
	}

	if state.SequenceNum != 100 {
		t.Errorf("expected sequence 100, got %d", state.SequenceNum)
	}

	if state.GetPhase() != pbft.Idle {
		t.Errorf("expected Idle phase, got %v", state.GetPhase())
	}
}

func TestStateTransitions(t *testing.T) {
	state := pbft.NewState(0, 1)

	// Create a mock block
	block := types.NewBlock(1, nil, "node1", 0, nil)

	// Create pre-prepare message
	prePrepareMsg := pbft.NewPrePrepareMsg(0, 1, block, "node1")

	// Set pre-prepare
	state.SetPrePrepare(prePrepareMsg, block)

	if state.GetPhase() != pbft.PrePrepared {
		t.Errorf("expected PrePrepared phase, got %v", state.GetPhase())
	}

	// Add prepare messages
	for i := 0; i < 3; i++ {
		prepare := pbft.NewPrepareMsg(0, 1, block.Hash, "node"+string(rune('1'+i)))
		state.AddPrepare(prepare)
	}

	if state.PrepareCount() != 3 {
		t.Errorf("expected 3 prepares, got %d", state.PrepareCount())
	}

	// Transition to prepared
	if state.IsPrepared(3) {
		state.TransitionToPrepared()
	}

	if state.GetPhase() != pbft.Prepared {
		t.Errorf("expected Prepared phase, got %v", state.GetPhase())
	}

	// Add commit messages
	for i := 0; i < 3; i++ {
		commit := pbft.NewCommitMsg(0, 1, block.Hash, "node"+string(rune('1'+i)))
		state.AddCommit(commit)
	}

	if state.CommitCount() != 3 {
		t.Errorf("expected 3 commits, got %d", state.CommitCount())
	}

	// Transition to committed
	if state.IsCommitted(3) {
		state.TransitionToCommitted()
	}

	if state.GetPhase() != pbft.Committed {
		t.Errorf("expected Committed phase, got %v", state.GetPhase())
	}
}

func TestValidatorSet(t *testing.T) {
	validators := []*types.Validator{
		{ID: "node1", Power: 1},
		{ID: "node2", Power: 1},
		{ID: "node3", Power: 1},
		{ID: "node4", Power: 1},
	}

	vs := types.NewValidatorSet(validators)

	if vs.Size() != 4 {
		t.Errorf("expected 4 validators, got %d", vs.Size())
	}

	// With 4 nodes: f = (4-1)/3 = 1, quorum = 2*1 + 1 = 3
	if vs.QuorumSize() != 3 {
		t.Errorf("expected quorum size 3, got %d", vs.QuorumSize())
	}

	if vs.FaultyTolerance() != 1 {
		t.Errorf("expected faulty tolerance 1, got %d", vs.FaultyTolerance())
	}

	// Test GetByID
	v := vs.GetByID("node2")
	if v == nil || v.ID != "node2" {
		t.Error("GetByID failed for node2")
	}

	v = vs.GetByID("nonexistent")
	if v != nil {
		t.Error("GetByID should return nil for nonexistent node")
	}
}

func TestBlock(t *testing.T) {
	txs := []types.Transaction{
		{ID: "tx1", Data: []byte("data1")},
		{ID: "tx2", Data: []byte("data2")},
	}

	block := types.NewBlock(1, nil, "node1", 0, txs)

	if block.Header.Height != 1 {
		t.Errorf("expected height 1, got %d", block.Header.Height)
	}

	if block.Header.ProposerID != "node1" {
		t.Errorf("expected proposer node1, got %s", block.Header.ProposerID)
	}

	if len(block.Transactions) != 2 {
		t.Errorf("expected 2 transactions, got %d", len(block.Transactions))
	}

	if block.Hash == nil {
		t.Error("block hash should not be nil")
	}

	// Test hash consistency
	hash1 := block.ComputeHash()
	hash2 := block.ComputeHash()
	if string(hash1) != string(hash2) {
		t.Error("block hash should be deterministic")
	}
}

func TestStateLog(t *testing.T) {
	stateLog := pbft.NewStateLog(100)

	// Test window bounds
	if !stateLog.IsInWindow(50) {
		t.Error("sequence 50 should be in window")
	}

	if stateLog.IsInWindow(0) {
		t.Error("sequence 0 should not be in window")
	}

	if stateLog.IsInWindow(101) {
		t.Error("sequence 101 should not be in window")
	}

	// Test getting state
	state := stateLog.GetState(0, 10)
	if state == nil {
		t.Error("GetState should create new state")
	}

	// Test getting existing state
	existingState := stateLog.GetExistingState(10)
	if existingState != state {
		t.Error("GetExistingState should return the same state")
	}

	// Test advancing watermarks
	stateLog.AdvanceWatermarks(50)

	if stateLog.IsInWindow(50) {
		t.Error("sequence 50 should not be in window after advancing")
	}

	if !stateLog.IsInWindow(100) {
		t.Error("sequence 100 should be in window after advancing")
	}
}

func TestMessageTypes(t *testing.T) {
	tests := []struct {
		msgType pbft.MessageType
		str     string
	}{
		{pbft.PrePrepare, "PRE-PREPARE"},
		{pbft.Prepare, "PREPARE"},
		{pbft.Commit, "COMMIT"},
		{pbft.ViewChange, "VIEW-CHANGE"},
		{pbft.NewView, "NEW-VIEW"},
	}

	for _, tt := range tests {
		if tt.msgType.String() != tt.str {
			t.Errorf("expected %s, got %s", tt.str, tt.msgType.String())
		}
	}
}

func TestMockTransport(t *testing.T) {
	mt := network.NewMockTransport("node1", []string{"node2", "node3"})

	msg := pbft.NewMessage(pbft.PrePrepare, 0, 1, []byte("digest"), "node1")

	// Test broadcast
	err := mt.Broadcast(msg)
	if err != nil {
		t.Errorf("broadcast failed: %v", err)
	}

	sent := mt.GetSentMessages()
	if len(sent) != 1 {
		t.Errorf("expected 1 sent message, got %d", len(sent))
	}

	// Test clear
	mt.ClearSentMessages()
	sent = mt.GetSentMessages()
	if len(sent) != 0 {
		t.Errorf("expected 0 sent messages after clear, got %d", len(sent))
	}
}

func TestApplication(t *testing.T) {
	app := abci.NewApplication()

	// Add transaction
	tx := types.Transaction{
		ID:   "tx1",
		Data: []byte(`{"type":"set","key":"foo","value":"bar"}`),
	}

	err := app.AddPendingTransaction(tx)
	if err != nil {
		t.Errorf("failed to add transaction: %v", err)
	}

	// Get pending transactions
	pending := app.GetPendingTransactions()
	if len(pending) != 1 {
		t.Errorf("expected 1 pending transaction, got %d", len(pending))
	}

	// Create and execute block
	block := types.NewBlock(1, nil, "node1", 0, pending)

	_, err = app.ExecuteBlock(block)
	if err != nil {
		t.Errorf("failed to execute block: %v", err)
	}

	// Commit block
	err = app.Commit(block)
	if err != nil {
		t.Errorf("failed to commit block: %v", err)
	}

	// Query state
	value, err := app.Query("foo")
	if err != nil {
		t.Errorf("failed to query: %v", err)
	}

	if string(value) != "bar" {
		t.Errorf("expected 'bar', got '%s'", string(value))
	}
}

func TestEngineCreation(t *testing.T) {
	validators := []*types.Validator{
		{ID: "node1", Power: 1},
		{ID: "node2", Power: 1},
		{ID: "node3", Power: 1},
		{ID: "node4", Power: 1},
	}
	vs := types.NewValidatorSet(validators)

	config := pbft.DefaultConfig("node1")
	transport := network.NewMockTransport("node1", []string{"node2", "node3", "node4"})
	app := abci.NewApplication()

	engine := pbft.NewEngine(config, vs, transport, app, nil)

	if engine == nil {
		t.Error("engine should not be nil")
	}

	if engine.GetCurrentView() != 0 {
		t.Errorf("expected view 0, got %d", engine.GetCurrentView())
	}

	if engine.GetCurrentHeight() != 0 {
		t.Errorf("expected height 0, got %d", engine.GetCurrentHeight())
	}
}

func TestViewChangeManager(t *testing.T) {
	vcm := pbft.NewViewChangeManager("node1", 3)

	if vcm.GetCurrentView() != 0 {
		t.Errorf("expected current view 0, got %d", vcm.GetCurrentView())
	}

	if vcm.IsInProgress() {
		t.Error("view change should not be in progress initially")
	}
}

func TestViewChangeTimer(t *testing.T) {
	triggered := false
	timer := pbft.NewViewChangeTimer(100*time.Millisecond, func() {
		triggered = true
	})

	timer.Start()
	time.Sleep(150 * time.Millisecond)

	if !triggered {
		t.Error("timer callback should have been triggered")
	}

	// Test stop
	triggered = false
	timer.Start()
	timer.Stop()
	time.Sleep(150 * time.Millisecond)

	if triggered {
		t.Error("timer callback should not trigger after stop")
	}
}
