// Package pbft implements the Practical Byzantine Fault Tolerance consensus algorithm.
package pbft

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/ahwlsqja/pbft-cosmos/metrics"
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

// 구현은 다른 파일에서 하짐나 "이런 기능이 필요하다" 정의
type Transport interface {
	// 모든 노드에게 전송
	Broadcast(msg *Message) error

	Send(nodeID string, msg *Message) error

	// SetMessageHandler sets the handler for incoming messages.
	SetMessageHandler(handler func(*Message))
}

// 앱만에 보내주는 것임 ABCI를 통해서 
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

//메인 엔진
type Engine struct {
	mu sync.RWMutex

	// 설정
	config *Config

	// 현재 뷰 번호
	view uint64

	// 현재 시퀸스 번호 블록 높이
	sequenceNum uint64

	// 검증자 목록
	validatorSet *types.ValidatorSet

	// 상태 로그 (state.go에 있음)
	stateLog *StateLog

	// 네트워크
	transport Transport

	//애플리케이션
	app Application

	// 모니터링
	metrics *metrics.Metrics

	// 메시지 채널
	msgChan chan *Message

	// 요청 채널
	requestChan chan *RequestMsg

	// 뷰 체인지 타이머
	viewChangeTimer *time.Timer

	// 종료 컨텍스트
	ctx    context.Context
	cancel context.CancelFunc

	// 로그
	logger *log.Logger

	// 확정된 블록들
	committedBlocks []*types.Block
}

// 엔진 생성 및 시작
func NewEngine(config *Config, validatorSet *types.ValidatorSet, transport Transport, app Application, m *metrics.Metrics) *Engine {
	ctx, cancel := context.WithCancel(context.Background())

	engine := &Engine{
		config:          config,
		view:            0,
		sequenceNum:     0,
		validatorSet:    validatorSet,
		stateLog:        NewStateLog(config.WindowSize),
		transport:       transport,
		app:             app,
		metrics:         m,
		msgChan:         make(chan *Message, 1000),
		requestChan:     make(chan *RequestMsg, 1000),
		ctx:             ctx,
		cancel:          cancel,
		logger:          log.Default(),
		committedBlocks: make([]*types.Block, 0),
	}

	// Set message handler
	if transport != nil {
		transport.SetMessageHandler(engine.handleIncomingMessage)
	}

	return engine
}

// PBFT 컨센서스 엔진 시작
func (e *Engine) Start() error {
	e.logger.Printf("[PBFT] Starting engine for node %s", e.config.NodeID)

	// 메인 루프 시작
	go e.run()

	// 뷰 체인지 타이머 시작
	e.resetViewChangeTimer()

	return nil
}

// 엔진 정지
func (e *Engine) Stop() {
	e.logger.Printf("[PBFT] Stopping engine for node %s", e.config.NodeID)
	e.cancel() // 컨센서스 취소 ->run() 종료
}

// 메인 컨센서스 루프 시작
// 1. ctx.Done(): 종료 신호
// 2. msgChan: 다른 노드에서 온 메시지 (Preprepare, Prepare, Commit 등)
// 3. requestChan: 클라이언트 요청 (트랜잭션)
// select = 여러 채널 중 먼저 오는 것 처리
func (e *Engine) run() {
	for {
		select {
		case <-e.ctx.Done():
			// Stop() 호출됨 -> 종료
			return

		case msg := <-e.msgChan:
			// 네트워크에서 메시지 도착
			e.handleMessage(msg)

		case req := <-e.requestChan:
			// 클라이언트 요청 도착
			if e.isPrimary() {
				e.proposeBlock(req) // 리더만 블록 제안
			}
		}
	}
}

// 네트워크에서 메시지 오면 채널에 넣음
// 채널 가득 차면 버림 (non-blocking)
func (e *Engine) handleIncomingMessage(msg *Message) {
	select {
	case e.msgChan <- msg:
		// 채널에 넣기 성공
	default:
		// 채널 가득 참 -> 버림
		e.logger.Printf("[PBFT] Message channel full, dropping message")
	}
}

// 메시지 타입에 따라 다른 핸들러 호출
func (e *Engine) handleMessage(msg *Message) {
	// 매트릭 측정 시작
	startTime := time.Now()
	defer func() {
		if e.metrics != nil {
			e.metrics.RecordMessageProcessingTime(msg.Type.String(), time.Since(startTime))
			e.metrics.IncrementMessagesReceived(msg.Type.String())
		}
	}()

	switch msg.Type {
	case PrePrepare:
		e.handlePrePrepare(msg)
	case Prepare:
		e.handlePrepare(msg)
	case Commit:
		e.handleCommit(msg)
	case ViewChange:
		e.handleViewChange(msg)
	case NewView:
		e.handleNewView(msg)
	default:
		e.logger.Printf("[PBFT] Unknown message type: %v", msg.Type)
	}
}

// isPrimary checks if this node is the primary for the current view.
func (e *Engine) isPrimary() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()

	primaryIdx := int(e.view) % len(e.validatorSet.Validators)
	return e.validatorSet.Validators[primaryIdx].ID == e.config.NodeID
}

// getPrimaryID returns the ID of the primary for the current view.
func (e *Engine) getPrimaryID() string {
	e.mu.RLock()
	defer e.mu.RUnlock()

	primaryIdx := int(e.view) % len(e.validatorSet.Validators)
	return e.validatorSet.Validators[primaryIdx].ID
}

// proposeBlock proposes a new block (primary only).
func (e *Engine) proposeBlock(req *RequestMsg) {
	e.mu.Lock()
	e.sequenceNum++
	seqNum := e.sequenceNum
	view := e.view
	e.mu.Unlock()

	// Check if sequence number is within window
	if !e.stateLog.IsInWindow(seqNum) {
		e.logger.Printf("[PBFT] Sequence number %d out of window", seqNum)
		return
	}

	// Get pending transactions
	var txs []types.Transaction
	if e.app != nil {
		txs = e.app.GetPendingTransactions()
	}

	// If we have a request, add it as a transaction
	if req != nil {
		txs = append(txs, types.Transaction{
			ID:        fmt.Sprintf("tx-%d", time.Now().UnixNano()),
			Data:      req.Operation,
			Timestamp: req.Timestamp,
			From:      req.ClientID,
		})
	}

	// Create new block
	var prevHash []byte
	if len(e.committedBlocks) > 0 {
		prevHash = e.committedBlocks[len(e.committedBlocks)-1].Hash
	}
	block := types.NewBlock(seqNum, prevHash, e.config.NodeID, view, txs)

	// Validate block
	if e.app != nil {
		if err := e.app.ValidateBlock(block); err != nil {
			e.logger.Printf("[PBFT] Block validation failed: %v", err)
			return
		}
	}

	// Create pre-prepare message
	prePrepareMsg := NewPrePrepareMsg(view, seqNum, block, e.config.NodeID)

	// Store in state log
	state := e.stateLog.GetState(view, seqNum)
	state.SetPrePrepare(prePrepareMsg, block)

	// Create network message
	payload, _ := json.Marshal(prePrepareMsg)
	msg := NewMessage(PrePrepare, view, seqNum, block.Hash, e.config.NodeID)
	msg.Payload = payload

	// Broadcast pre-prepare
	e.broadcast(msg)

	e.logger.Printf("[PBFT] Primary broadcast PRE-PREPARE for seq %d", seqNum)

	if e.metrics != nil {
		e.metrics.StartConsensusRound(seqNum)
	}
}

// handlePrePrepare handles a pre-prepare message.
func (e *Engine) handlePrePrepare(msg *Message) {
	// Verify sender is the primary
	if msg.NodeID != e.getPrimaryID() {
		e.logger.Printf("[PBFT] Received PRE-PREPARE from non-primary %s", msg.NodeID)
		return
	}

	// Verify view number
	e.mu.RLock()
	currentView := e.view
	e.mu.RUnlock()

	if msg.View != currentView {
		e.logger.Printf("[PBFT] PRE-PREPARE view mismatch: got %d, expected %d", msg.View, currentView)
		return
	}

	// Check if sequence number is within window
	if !e.stateLog.IsInWindow(msg.SequenceNum) {
		e.logger.Printf("[PBFT] PRE-PREPARE seq %d out of window", msg.SequenceNum)
		return
	}

	// Decode pre-prepare message
	var prePrepareMsg PrePrepareMsg
	if err := json.Unmarshal(msg.Payload, &prePrepareMsg); err != nil {
		e.logger.Printf("[PBFT] Failed to decode PRE-PREPARE: %v", err)
		return
	}

	// Validate block
	if e.app != nil {
		if err := e.app.ValidateBlock(prePrepareMsg.Block); err != nil {
			e.logger.Printf("[PBFT] Block validation failed: %v", err)
			return
		}
	}

	// Store in state log
	state := e.stateLog.GetState(msg.View, msg.SequenceNum)
	state.SetPrePrepare(&prePrepareMsg, prePrepareMsg.Block)

	// Send prepare message
	prepareMsg := NewPrepareMsg(msg.View, msg.SequenceNum, msg.Digest, e.config.NodeID)
	payload, _ := json.Marshal(prepareMsg)
	prepareNetMsg := NewMessage(Prepare, msg.View, msg.SequenceNum, msg.Digest, e.config.NodeID)
	prepareNetMsg.Payload = payload

	e.broadcast(prepareNetMsg)

	// Reset view change timer
	e.resetViewChangeTimer()

	e.logger.Printf("[PBFT] Node %s sent PREPARE for seq %d", e.config.NodeID, msg.SequenceNum)
}

// handlePrepare handles a prepare message.
func (e *Engine) handlePrepare(msg *Message) {
	// Verify view number
	e.mu.RLock()
	currentView := e.view
	e.mu.RUnlock()

	if msg.View != currentView {
		return
	}

	// Decode prepare message
	var prepareMsg PrepareMsg
	if err := json.Unmarshal(msg.Payload, &prepareMsg); err != nil {
		e.logger.Printf("[PBFT] Failed to decode PREPARE: %v", err)
		return
	}

	// Get state
	state := e.stateLog.GetExistingState(msg.SequenceNum)
	if state == nil {
		return
	}

	// Verify digest matches pre-prepare
	if state.PrePrepareMsg == nil || !bytes.Equal(state.PrePrepareMsg.Digest, msg.Digest) {
		e.logger.Printf("[PBFT] PREPARE digest mismatch for seq %d", msg.SequenceNum)
		return
	}

	// Add prepare message
	state.AddPrepare(&prepareMsg)

	// Check if we have 2f+1 prepares (quorum)
	quorum := e.validatorSet.QuorumSize()
	if state.IsPrepared(quorum) && state.GetPhase() == PrePrepared {
		state.TransitionToPrepared()

		// Send commit message
		commitMsg := NewCommitMsg(msg.View, msg.SequenceNum, msg.Digest, e.config.NodeID)
		payload, _ := json.Marshal(commitMsg)
		commitNetMsg := NewMessage(Commit, msg.View, msg.SequenceNum, msg.Digest, e.config.NodeID)
		commitNetMsg.Payload = payload

		e.broadcast(commitNetMsg)

		e.logger.Printf("[PBFT] Node %s PREPARED and sent COMMIT for seq %d (prepares: %d)",
			e.config.NodeID, msg.SequenceNum, state.PrepareCount())
	}
}

// handleCommit handles a commit message.
func (e *Engine) handleCommit(msg *Message) {
	// Verify view number
	e.mu.RLock()
	currentView := e.view
	e.mu.RUnlock()

	if msg.View != currentView {
		return
	}

	// Decode commit message
	var commitMsg CommitMsg
	if err := json.Unmarshal(msg.Payload, &commitMsg); err != nil {
		e.logger.Printf("[PBFT] Failed to decode COMMIT: %v", err)
		return
	}

	// Get state
	state := e.stateLog.GetExistingState(msg.SequenceNum)
	if state == nil {
		return
	}

	// Add commit message
	state.AddCommit(&commitMsg)

	// Check if we have 2f+1 commits (quorum)
	quorum := e.validatorSet.QuorumSize()
	if state.IsCommitted(quorum) && state.GetPhase() == Prepared {
		state.TransitionToCommitted()
		e.executeBlock(state)

		e.logger.Printf("[PBFT] Node %s COMMITTED seq %d (commits: %d)",
			e.config.NodeID, msg.SequenceNum, state.CommitCount())
	}
}

// executeBlock executes a committed block.
func (e *Engine) executeBlock(state *State) {
	if state.Executed || state.Block == nil {
		return
	}

	startTime := time.Now()

	// Execute block
	if e.app != nil {
		_, err := e.app.ExecuteBlock(state.Block)
		if err != nil {
			e.logger.Printf("[PBFT] Block execution failed: %v", err)
			return
		}

		// Commit to state
		if err := e.app.Commit(state.Block); err != nil {
			e.logger.Printf("[PBFT] Block commit failed: %v", err)
			return
		}
	}

	// Mark as executed
	state.MarkExecuted()

	// Add to committed blocks
	e.mu.Lock()
	e.committedBlocks = append(e.committedBlocks, state.Block)
	e.mu.Unlock()

	// Record metrics
	if e.metrics != nil {
		e.metrics.EndConsensusRound(state.SequenceNum)
		e.metrics.SetBlockHeight(state.SequenceNum)
		e.metrics.RecordBlockExecutionTime(time.Since(startTime))
		e.metrics.AddTransactions(len(state.Block.Transactions))
	}

	e.logger.Printf("[PBFT] Executed block at height %d with %d txs",
		state.SequenceNum, len(state.Block.Transactions))

	// Check if we need to create a checkpoint
	if state.SequenceNum%e.config.CheckpointInterval == 0 {
		e.createCheckpoint(state.SequenceNum)
	}
}

// createCheckpoint creates a stable checkpoint.
func (e *Engine) createCheckpoint(seqNum uint64) {
	// Advance water marks
	e.stateLog.AdvanceWatermarks(seqNum)

	e.logger.Printf("[PBFT] Created checkpoint at seq %d", seqNum)
}

// handleViewChange handles a view change message.
func (e *Engine) handleViewChange(msg *Message) {
	// TODO: Implement view change logic
	e.logger.Printf("[PBFT] Received VIEW-CHANGE from %s for view %d", msg.NodeID, msg.View)
}

// handleNewView handles a new view message.
func (e *Engine) handleNewView(msg *Message) {
	// TODO: Implement new view logic
	e.logger.Printf("[PBFT] Received NEW-VIEW from %s for view %d", msg.NodeID, msg.View)
}

// startViewChange initiates a view change.
func (e *Engine) startViewChange() {
	e.mu.Lock()
	newView := e.view + 1
	e.mu.Unlock()

	e.logger.Printf("[PBFT] Starting view change to view %d", newView)

	if e.metrics != nil {
		e.metrics.IncrementViewChanges()
	}

	// TODO: Implement full view change protocol
}

// resetViewChangeTimer resets the view change timer.
func (e *Engine) resetViewChangeTimer() {
	if e.viewChangeTimer != nil {
		e.viewChangeTimer.Stop()
	}
	e.viewChangeTimer = time.AfterFunc(e.config.ViewChangeTimeout, func() {
		e.startViewChange()
	})
}

// broadcast sends a message to all nodes.
func (e *Engine) broadcast(msg *Message) {
	if e.transport != nil {
		if err := e.transport.Broadcast(msg); err != nil {
			e.logger.Printf("[PBFT] Broadcast failed: %v", err)
		}
	}

	if e.metrics != nil {
		e.metrics.IncrementMessagesSent(msg.Type.String())
	}
}

// SubmitRequest submits a new request to the consensus engine.
func (e *Engine) SubmitRequest(operation []byte, clientID string) error {
	req := &RequestMsg{
		Operation: operation,
		Timestamp: time.Now(),
		ClientID:  clientID,
	}

	select {
	case e.requestChan <- req:
		return nil
	default:
		return fmt.Errorf("request channel full")
	}
}

// GetCurrentView returns the current view number.
func (e *Engine) GetCurrentView() uint64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.view
}

// GetCurrentHeight returns the current block height.
func (e *Engine) GetCurrentHeight() uint64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.sequenceNum
}

// GetCommittedBlocks returns all committed blocks.
func (e *Engine) GetCommittedBlocks() []*types.Block {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.committedBlocks
}
