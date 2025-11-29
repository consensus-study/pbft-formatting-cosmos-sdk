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

	// 뷰 체인지 매니저 (view_change.go)
	viewChangeManager *ViewChangeManager

	// 체크포인트 저장소 (seqNum -> digest)
	checkpoints map[uint64][]byte

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
		checkpoints:     make(map[uint64][]byte),
		ctx:             ctx,
		cancel:          cancel,
		logger:          log.Default(),
		committedBlocks: make([]*types.Block, 0),
	}

	// ViewChangeManager 초기화
	engine.viewChangeManager = NewViewChangeManager(config.NodeID, validatorSet.QuorumSize())

	// ViewChangeManager에 브로드캐스트 함수 연결
	engine.viewChangeManager.SetBroadcastFunc(engine.broadcast)

	// ViewChange 완료 시 콜백 설정
	engine.viewChangeManager.SetOnViewChangeComplete(engine.onViewChangeComplete)

	// Set message handler
	if transport != nil {
		transport.SetMessageHandler(engine.handleIncomingMessage)
	}

	return engine
}

// onViewChangeComplete - 뷰 체인지 완료 시 호출되는 콜백
func (e *Engine) onViewChangeComplete(newView uint64) {
	e.mu.Lock()
	e.view = newView
	e.mu.Unlock()

	e.logger.Printf("[PBFT] View change completed. New view: %d", newView)

	// 메트릭 업데이트
	if e.metrics != nil {
		e.metrics.SetCurrentView(newView)
	}

	// 타이머 리셋
	e.resetViewChangeTimer()

	// 내가 새 리더면 대기 중인 요청 처리
	if e.isPrimary() {
		e.logger.Printf("[PBFT] I am the new primary for view %d", newView)
	}
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

	// 메세지 타입별 분기
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

// 리더 확인
func (e *Engine) isPrimary() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()

	// View % 노드수 = 리더 인덱스
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

// 리더가 블록제안
func (e *Engine) proposeBlock(req *RequestMsg) {
	e.mu.Lock()
	e.sequenceNum++
	// 블록높이 ++ 
	seqNum := e.sequenceNum
	// 리더 저장
	view := e.view
	e.mu.Unlock()

	// 블록 높이가 윈도우에 있는지 체크
	if !e.stateLog.IsInWindow(seqNum) {
		e.logger.Printf("[PBFT] Sequence number %d out of window", seqNum)
		return
	}

	// 트랜잭션 수집
	var txs []types.Transaction
	if e.app != nil {
		txs = e.app.GetPendingTransactions()
	}

	// 요청이 있으면 트랜잭션으로 추가
	if req != nil {
		txs = append(txs, types.Transaction{
			ID:        fmt.Sprintf("tx-%d", time.Now().UnixNano()),
			Data:      req.Operation,
			Timestamp: req.Timestamp,
			From:      req.ClientID,
		})
	}

	// 블록 생성
	var prevHash []byte
	if len(e.committedBlocks) > 0 {
		prevHash = e.committedBlocks[len(e.committedBlocks)-1].Hash
	}
	block := types.NewBlock(seqNum, prevHash, e.config.NodeID, view, txs)

	// 블록 검증
	if e.app != nil {
		if err := e.app.ValidateBlock(block); err != nil {
			e.logger.Printf("[PBFT] Block validation failed: %v", err)
			return
		}
	}

	//투표준비됨 메시지 생성
	prePrepareMsg := NewPrePrepareMsg(view, seqNum, block, e.config.NodeID)

	// StateLog에 저장
	state := e.stateLog.GetState(view, seqNum)
	state.SetPrePrepare(prePrepareMsg, block)

	// 네트워크 메시지 생성 및 브로드캐스트
	payload, _ := json.Marshal(prePrepareMsg)
	msg := NewMessage(PrePrepare, view, seqNum, block.Hash, e.config.NodeID)
	msg.Payload = payload

	e.broadcast(msg)

	e.logger.Printf("[PBFT] Primary broadcast PRE-PREPARE for seq %d", seqNum)

	if e.metrics != nil {
		e.metrics.StartConsensusRound(seqNum)
	}
}

// 다른 노드가 받음
func (e *Engine) handlePrePrepare(msg *Message) {
	// 리더가 보낸건지 확인
	if msg.NodeID != e.getPrimaryID() {
		e.logger.Printf("[PBFT] Received PRE-PREPARE from non-primary %s", msg.NodeID)
		return
	}

	// 뷰 번호 확인
	e.mu.RLock()
	currentView := e.view
	e.mu.RUnlock()

	if msg.View != currentView {
		e.logger.Printf("[PBFT] PRE-PREPARE view mismatch: got %d, expected %d", msg.View, currentView)
		return
	}

	// 윈도우 체크
	if !e.stateLog.IsInWindow(msg.SequenceNum) {
		e.logger.Printf("[PBFT] PRE-PREPARE seq %d out of window", msg.SequenceNum)
		return
	}

	// 디코딩함 메시지를
	var prePrepareMsg PrePrepareMsg
	if err := json.Unmarshal(msg.Payload, &prePrepareMsg); err != nil {
		e.logger.Printf("[PBFT] Failed to decode PRE-PREPARE: %v", err)
		return
	}

	// 블록 검증
	if e.app != nil {
		if err := e.app.ValidateBlock(prePrepareMsg.Block); err != nil {
			e.logger.Printf("[PBFT] Block validation failed: %v", err)
			return
		}
	}

	// StateLog에 저장
	state := e.stateLog.GetState(msg.View, msg.SequenceNum)
	state.SetPrePrepare(&prePrepareMsg, prePrepareMsg.Block)

	// Prepare 메시지 생성 및 브로드 캐스트
	prepareMsg := NewPrepareMsg(msg.View, msg.SequenceNum, msg.Digest, e.config.NodeID)
	payload, _ := json.Marshal(prepareMsg)
	prepareNetMsg := NewMessage(Prepare, msg.View, msg.SequenceNum, msg.Digest, e.config.NodeID)
	prepareNetMsg.Payload = payload

	e.broadcast(prepareNetMsg)

	// Reset view change timer
	e.resetViewChangeTimer()

	e.logger.Printf("[PBFT] Node %s sent PREPARE for seq %d", e.config.NodeID, msg.SequenceNum)
}

// 투표
func (e *Engine) handlePrepare(msg *Message) {
	// 뷰 번호 확인
	e.mu.RLock()
	currentView := e.view
	e.mu.RUnlock()

	if msg.View != currentView {
		return
	}

	// 메시지 디코딩
	var prepareMsg PrepareMsg
	if err := json.Unmarshal(msg.Payload, &prepareMsg); err != nil {
		e.logger.Printf("[PBFT] Failed to decode PREPARE: %v", err)
		return
	}

	// State 가져오기
	state := e.stateLog.GetExistingState(msg.SequenceNum)
	if state == nil {
		return
	}

	// Digest 확인 (같은 블록에 대한 건지)
	if state.PrePrepareMsg == nil || !bytes.Equal(state.PrePrepareMsg.Digest, msg.Digest) {
		e.logger.Printf("[PBFT] PREPARE digest mismatch for seq %d", msg.SequenceNum)
		return
	}

	// quorum(2f+1) 확인
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
	e.mu.Lock()
	// 체크포인트 저장
	if len(e.committedBlocks) > 0 {
		lastBlock := e.committedBlocks[len(e.committedBlocks)-1]
		e.checkpoints[seqNum] = lastBlock.Hash
	}

	// 오래된 체크포인트 정리 (최근 3개만 유지)
	if len(e.checkpoints) > 3 {
		var oldestSeq uint64 = seqNum
		for s := range e.checkpoints {
			if s < oldestSeq {
				oldestSeq = s
			}
		}
		delete(e.checkpoints, oldestSeq)
	}
	e.mu.Unlock()

	// Advance water marks
	e.stateLog.AdvanceWatermarks(seqNum)

	e.logger.Printf("[PBFT] Created checkpoint at seq %d", seqNum)
}

// handleViewChange handles a view change message.
// 다른 노드가 보낸 ViewChange 메시지를 처리
func (e *Engine) handleViewChange(msg *Message) {
	e.logger.Printf("[PBFT] Received VIEW-CHANGE from %s for view %d", msg.NodeID, msg.View)

	// 메시지 디코딩
	var viewChangeMsg ViewChangeMsg
	if err := json.Unmarshal(msg.Payload, &viewChangeMsg); err != nil {
		e.logger.Printf("[PBFT] Failed to decode VIEW-CHANGE: %v", err)
		return
	}

	// 현재 뷰보다 높은 뷰에 대한 것만 처리
	e.mu.RLock()
	currentView := e.view
	e.mu.RUnlock()

	if viewChangeMsg.NewView <= currentView {
		e.logger.Printf("[PBFT] Ignoring VIEW-CHANGE for old view %d (current: %d)",
			viewChangeMsg.NewView, currentView)
		return
	}

	// ViewChangeManager에 전달
	hasQuorum := e.viewChangeManager.HandleViewChange(&viewChangeMsg)

	if hasQuorum {
		e.logger.Printf("[PBFT] Got quorum for view %d", viewChangeMsg.NewView)

		// 내가 새 뷰의 리더인지 확인
		newPrimaryIdx := int(viewChangeMsg.NewView) % len(e.validatorSet.Validators)
		newPrimaryID := e.validatorSet.Validators[newPrimaryIdx].ID

		if newPrimaryID == e.config.NodeID {
			// 내가 새 리더 -> NewView 메시지 생성 및 브로드캐스트
			e.broadcastNewView(viewChangeMsg.NewView)
		}
	}
}

// handleNewView handles a new view message.
// 새 리더가 보낸 NewView 메시지를 처리
func (e *Engine) handleNewView(msg *Message) {
	e.logger.Printf("[PBFT] Received NEW-VIEW from %s for view %d", msg.NodeID, msg.View)

	// 메시지 디코딩
	var newViewMsg NewViewMsg
	if err := json.Unmarshal(msg.Payload, &newViewMsg); err != nil {
		e.logger.Printf("[PBFT] Failed to decode NEW-VIEW: %v", err)
		return
	}

	// 현재 뷰보다 높은 뷰에 대한 것만 처리
	e.mu.RLock()
	currentView := e.view
	e.mu.RUnlock()

	if newViewMsg.View <= currentView {
		e.logger.Printf("[PBFT] Ignoring NEW-VIEW for old view %d (current: %d)",
			newViewMsg.View, currentView)
		return
	}

	// 새 리더가 맞는지 확인
	expectedPrimaryIdx := int(newViewMsg.View) % len(e.validatorSet.Validators)
	expectedPrimaryID := e.validatorSet.Validators[expectedPrimaryIdx].ID

	if newViewMsg.NewPrimaryID != expectedPrimaryID {
		e.logger.Printf("[PBFT] NEW-VIEW from wrong primary: got %s, expected %s",
			newViewMsg.NewPrimaryID, expectedPrimaryID)
		return
	}

	// ViewChangeManager에서 검증 및 처리
	if e.viewChangeManager.HandleNewView(&newViewMsg) {
		e.logger.Printf("[PBFT] NEW-VIEW accepted for view %d", newViewMsg.View)

		// 뷰 업데이트 (콜백에서 처리됨)

		// NewView에 포함된 PrePrepare 메시지들 처리
		// (이전 뷰에서 Prepared 상태였던 블록들)
		for _, prePrepare := range newViewMsg.PrePrepareMsgs {
			e.reprocessPrePrepare(&prePrepare, newViewMsg.View)
		}
	} else {
		e.logger.Printf("[PBFT] NEW-VIEW rejected for view %d", newViewMsg.View)
	}
}

// reprocessPrePrepare - 뷰 체인지 후 이전 PrePrepare 재처리
func (e *Engine) reprocessPrePrepare(prePrepare *PrePrepareMsg, newView uint64) {
	// 새 뷰 번호로 업데이트
	prePrepare.View = newView

	// StateLog에 저장
	state := e.stateLog.GetState(newView, prePrepare.SequenceNum)
	state.SetPrePrepare(prePrepare, prePrepare.Block)

	// Prepare 메시지 브로드캐스트
	prepareMsg := NewPrepareMsg(newView, prePrepare.SequenceNum, prePrepare.Digest, e.config.NodeID)
	payload, _ := json.Marshal(prepareMsg)
	prepareNetMsg := NewMessage(Prepare, newView, prePrepare.SequenceNum, prePrepare.Digest, e.config.NodeID)
	prepareNetMsg.Payload = payload

	e.broadcast(prepareNetMsg)

	e.logger.Printf("[PBFT] Reprocessed PRE-PREPARE for seq %d in new view %d",
		prePrepare.SequenceNum, newView)
}

// broadcastNewView - 새 리더가 NewView 메시지 브로드캐스트
func (e *Engine) broadcastNewView(newView uint64) {
	// NewView 메시지 생성
	newViewMsg := e.viewChangeManager.CreateNewViewMsg(newView, len(e.validatorSet.Validators))
	if newViewMsg == nil {
		e.logger.Printf("[PBFT] Failed to create NEW-VIEW message")
		return
	}

	// 브로드캐스트
	payload, _ := json.Marshal(newViewMsg)
	msg := NewMessage(NewView, newView, e.sequenceNum, nil, e.config.NodeID)
	msg.Payload = payload

	e.broadcast(msg)

	// 자신도 NewView 처리
	e.viewChangeManager.HandleNewView(newViewMsg)

	e.logger.Printf("[PBFT] Broadcast NEW-VIEW for view %d", newView)
}

// startViewChange initiates a view change.
// 타임아웃 시 호출되어 뷰 체인지 시작
func (e *Engine) startViewChange() {
	e.mu.Lock()
	newView := e.view + 1
	lastSeqNum := e.sequenceNum
	e.mu.Unlock()

	e.logger.Printf("[PBFT] Starting view change to view %d (timeout)", newView)

	if e.metrics != nil {
		e.metrics.IncrementViewChanges()
	}

	// 체크포인트 수집
	checkpoints := e.collectCheckpoints()

	// Prepared 상태인 블록들 수집
	preparedSet := e.collectPreparedCertificates()

	// ViewChangeManager를 통해 ViewChange 메시지 브로드캐스트
	e.viewChangeManager.StartViewChange(newView, lastSeqNum, checkpoints, preparedSet)

	// 타이머 재시작 (더 긴 타임아웃으로)
	// 뷰 체인지가 반복되면 타임아웃을 점점 늘림 (exponential backoff)
	e.mu.Lock()
	e.viewChangeTimer = time.AfterFunc(e.config.ViewChangeTimeout*2, func() {
		e.startViewChange()
	})
	e.mu.Unlock()
}

// collectCheckpoints - 저장된 체크포인트들 수집
func (e *Engine) collectCheckpoints() []Checkpoint {
	e.mu.RLock()
	defer e.mu.RUnlock()

	var checkpoints []Checkpoint
	for seqNum, digest := range e.checkpoints {
		checkpoints = append(checkpoints, Checkpoint{
			SequenceNum: seqNum,
			Digest:      digest,
			NodeID:      e.config.NodeID,
		})
	}
	return checkpoints
}

// collectPreparedCertificates - Prepared 상태인 블록들의 인증서 수집
func (e *Engine) collectPreparedCertificates() []PreparedCert {
	quorum := e.validatorSet.QuorumSize()
	var preparedCerts []PreparedCert

	// StateLog에서 Prepared 상태인 것들 찾기
	e.mu.RLock()
	view := e.view
	e.mu.RUnlock()

	// 현재 윈도우 내의 상태들 검사
	for seqNum := e.stateLog.LowWaterMark + 1; seqNum <= e.stateLog.HighWaterMark; seqNum++ {
		state := e.stateLog.GetExistingState(seqNum)
		if state == nil {
			continue
		}

		// Prepared 상태이고 아직 실행되지 않은 것
		if state.IsPrepared(quorum) && !state.Executed && state.PrePrepareMsg != nil {
			// Prepare 메시지들 수집
			var prepares []PrepareMsg
			for _, p := range state.PrepareMsgs {
				prepares = append(prepares, *p)
			}

			cert := PreparedCert{
				PrePrepare: PrePrepareMsg{
					View:        view,
					SequenceNum: seqNum,
					Digest:      state.PrePrepareMsg.Digest,
					Block:       state.Block,
					PrimaryID:   state.PrePrepareMsg.PrimaryID,
				},
				Prepares: prepares,
			}
			preparedCerts = append(preparedCerts, cert)
		}
	}

	return preparedCerts
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
