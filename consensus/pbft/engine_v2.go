// Package pbft provides PBFT consensus engine with ABCI 2.0 support.
// engine_v2.go - Cosmos SDK v0.53.0 + CometBFT v0.38.x 호환 버전
package pbft

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"

	"github.com/ahwlsqja/pbft-cosmos/mempool"
	"github.com/ahwlsqja/pbft-cosmos/metrics"
	"github.com/ahwlsqja/pbft-cosmos/types"
)

// EngineV2 - ABCI 2.0 호환 PBFT 엔진
type EngineV2 struct {
	mu sync.RWMutex

	config      *Config
	view        uint64 // 현재 뷰
	sequenceNum uint64 // 현재 블록 높이

	validatorSet *types.ValidatorSet // 검증자 목록
	stateLog     *StateLog           // 상태 저장소

	transport Transport // P2P 통신

	// ABCI 2.0 어댑터 (기존 Application 대신)
	abciAdapter ABCIAdapterInterface // ABCI 어댑터

	// Mempool - 트랜잭션 풀 (선택적)
	mempool *mempool.Mempool // 트랜잭션 풀

	metrics *metrics.Metrics

	msgChan     chan *Message    // 메시지수신채널
	requestChan chan *RequestMsg // 요청 수신 채널

	viewChangeTimer   *time.Timer
	viewChangeManager *ViewChangeManager // 뷰 체인지 관리자

	checkpoints map[uint64][]byte

	ctx    context.Context
	cancel context.CancelFunc

	logger *log.Logger

	committedBlocks []*types.Block
	lastAppHash     []byte

	// 체인 정보
	chainID string
}

// ABCIAdapterInterface - ABCI 어댑터 인터페이스
type ABCIAdapterInterface interface {
	Close() error
	InitChain(ctx context.Context, chainID string, validators []*types.Validator, appState []byte) error
	PrepareProposal(ctx context.Context, height int64, proposer []byte, txs [][]byte) ([][]byte, error)
	ProcessProposal(ctx context.Context, height int64, proposer []byte, txs [][]byte, hash []byte) (bool, error)
	FinalizeBlock(ctx context.Context, block *types.Block) (*ABCIExecutionResult, error)
	Commit(ctx context.Context) (appHash []byte, retainHeight int64, err error)
	CheckTx(ctx context.Context, tx []byte) error
	GetLastAppHash() []byte
	GetLastHeight() int64
	SetLastAppHash(hash []byte)
}

// NewEngineV2 - ABCI 2.0 호환 엔진 생성
func NewEngineV2(
	config *Config,
	validatorSet *types.ValidatorSet,
	transport Transport,
	abciAdapter ABCIAdapterInterface,
	m *metrics.Metrics,
) (*EngineV2, error) {
	ctx, cancel := context.WithCancel(context.Background())

	engine := &EngineV2{
		config:          config,
		view:            0,
		sequenceNum:     0,
		validatorSet:    validatorSet,
		stateLog:        NewStateLog(config.WindowSize),
		transport:       transport,
		abciAdapter:     abciAdapter,
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
	engine.viewChangeManager.SetBroadcastFunc(engine.broadcast)
	engine.viewChangeManager.SetOnViewChangeComplete(engine.onViewChangeComplete)

	if transport != nil {
		transport.SetMessageHandler(engine.handleIncomingMessage)
	}

	return engine, nil
}

// NewEngineV2WithABCIAddress - ABCI 주소로 엔진 생성
func NewEngineV2WithABCIAddress(
	config *Config,
	validatorSet *types.ValidatorSet,
	transport Transport,
	abciAddress string,
	m *metrics.Metrics,
) (*EngineV2, error) {
	abciAdapter, err := NewABCIAdapter(abciAddress)
	if err != nil {
		return nil, fmt.Errorf("failed to create ABCI adapter: %w", err)
	}

	return NewEngineV2(config, validatorSet, transport, abciAdapter, m)
}

// InitChain - 체인 초기화 (시작 시 호출)
func (e *EngineV2) InitChain(ctx context.Context, chainID string, appState []byte) error {
	e.chainID = chainID

	validators := e.validatorSet.Validators

	return e.abciAdapter.InitChain(ctx, chainID, validators, appState)
}

// Start - 엔진 시작
func (e *EngineV2) Start() error {
	e.logger.Printf("[PBFT-V2] Starting engine for node %s", e.config.NodeID)

	go e.run()
	e.resetViewChangeTimer()

	return nil
}

// Stop - 엔진 정지
func (e *EngineV2) Stop() {
	e.logger.Printf("[PBFT-V2] Stopping engine for node %s", e.config.NodeID)
	e.cancel()
}

// Close - 엔진 종료 (리소스 정리)
func (e *EngineV2) Close() error {
	e.Stop()
	if e.abciAdapter != nil {
		return e.abciAdapter.Close()
	}
	return nil
}

// run - 메인 컨센서스 루프
func (e *EngineV2) run() {
	for {
		select {
		case <-e.ctx.Done():
			return
		case msg := <-e.msgChan:
			// 메시지 왔을 때 handleMessage 실행함
			e.handleMessage(msg)
		case req := <-e.requestChan:
			// 리더 노드일 경우 블록 제안
			if e.isPrimary() {
				e.proposeBlock(req)
			}
		}
	}
}

// handleIncomingMessage - 네트워크에서 메시지 수신
func (e *EngineV2) handleIncomingMessage(msg *Message) {
	select {
	case e.msgChan <- msg:
	default:
		e.logger.Printf("[PBFT-V2] Message channel full, dropping message")
	}
}

// handleMessage - 메시지 처리
func (e *EngineV2) handleMessage(msg *Message) {
	// 시작 시간 찍음
	startTime := time.Now()
	// 이 함수 끝날 때 time.Since(startTime)이 함수 종료 시점에 계산되게 할려고 함.
	defer func() {
		if e.metrics != nil {
			e.metrics.RecordMessageProcessingTime(msg.Type.String(), time.Since(startTime))
			e.metrics.IncrementMessagesReceived(msg.Type.String())
		}
	}()

	switch msg.Type {
	case PrePrepare:
		// 메시지의 타입이 Preprepare을 받은 것일 때
		e.handlePrePrepare(msg)
	case Prepare:
		// 메시지의 타입이 prepare을 받을 것일 때
		e.handlePrepare(msg) 
	case Commit:
		// 메시지의 타입이 commit을 받을 것일 때
		e.handleCommit(msg)
	case ViewChange:
		// 메시지의 타입이 viewchange을 받을 것일 때
		e.handleViewChange(msg)
	case NewView:
		// 메시지의 타입이 newchange을 받을 것일 때
		e.handleNewView(msg)
	default:
		e.logger.Printf("[PBFT-V2] Unknown message type: %v", msg.Type)
	}
}

// isPrimary - 현재 노드가 리더인지 확인
func (e *EngineV2) isPrimary() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()

	primaryIdx := int(e.view) % len(e.validatorSet.Validators)
	return e.validatorSet.Validators[primaryIdx].ID == e.config.NodeID
}

// getPrimaryID - 현재 리더 ID 반환
func (e *EngineV2) getPrimaryID() string {
	e.mu.RLock()
	defer e.mu.RUnlock()

	primaryIdx := int(e.view) % len(e.validatorSet.Validators)
	return e.validatorSet.Validators[primaryIdx].ID
}

// proposeBlock - 블록 제안 (리더만, ABCI PrepareProposal 사용) -> prepropare 과정임.
// 리더가 나머지 노드한테 prepropare 브로드캐스트함. 처음 과정
func (e *EngineV2) proposeBlock(req *RequestMsg) {
	// 락 걸고
	e.mu.Lock()
	// 엔진의 블록높이 +1
	e.sequenceNum++
	// seqNum에 값 복사
	seqNum := e.sequenceNum
	// view에 값 복사
	view := e.view
	// mempool 복사
	mp := e.mempool
	// 락 풀음
	e.mu.Unlock()

	// 만약 IsInWindow
	if !e.stateLog.IsInWindow(seqNum) {
		e.logger.Printf("[PBFT-V2] Sequence number %d out of window", seqNum)
		return
	}

	// 트랜잭션 수집 - Mempool에서 가져오거나, 없으면 단일 요청 사용
	var txs [][]byte
	// 맴풀이 존재하면
	if mp != nil {
		// Mempool에서 최대 500개 트랜잭션을 FIFO 순서로 가져옴 abci에서 하고 블록 정렬하고 하는건 앱단에서 함.
		mempoolTxs := mp.ReapMaxTxs(500)
		// txs 에 쌓음
		for _, tx := range mempoolTxs {
			txs = append(txs, tx.Data)
		}
		
		e.logger.Printf("[PBFT-V2] Reaped %d txs from mempool for block %d", len(txs), seqNum)
	}

	// Mempool에서 가져온 트랜잭션이 없고, 직접 요청이 있으면 추가
	if len(txs) == 0 && req != nil {
		txs = append(txs, req.Operation)
	}

	// 트랜잭션이 없으면 빈 블록 생성하지 않음 (선택적)
	if len(txs) == 0 {
		e.logger.Printf("[PBFT-V2] No transactions to propose for block %d", seqNum)
		e.mu.Lock()
		e.sequenceNum-- // 롤백
		e.mu.Unlock()
		return
	}

	// ABCI PrepareProposal 호출 - 앱에게 트랜잭션 정렬/필터링 요청 사실상 엔진에서는 맴풀에 트랜잭션 모아놓고 제안할 때 던져준다음에 순서 정렬 시킨다 왜? 상태가 앱에 있으니까 
	// 1) 5초 타임 아웃 컨텍스트를 생성한다고 생각하면 된다
	ctx, cancel := context.WithTimeout(e.ctx, 5*time.Second)
	// 2) 함수 종료시 컨텍스트 취소
	defer cancel()

	// 3) 제안자 ID를 바이트로 변환
	proposer := []byte(e.config.NodeID)
	// 4) ABCI 앱에 요청
	preparedTxs, err := e.abciAdapter.PrepareProposal(ctx, int64(seqNum), proposer, txs)
	if err != nil {
		e.logger.Printf("[PBFT-V2] PrepareProposal failed: %v", err)
		return
	}

	// 5) 블록 생성
	// 이건 이전 블록의 해시임.
	var prevHash []byte
	// 엔진의 커밋된 블록이 존재하면 
	// 그 커밋된 블록의 해쉬를 prevHash에 저장함. 그니까 마지막 확정 블록 해쉬를 꺼내는 거임.
	// 왜냐하면 블록체인은 이전해시를 참조하는 식으로 링크드리스트가 되기 때문이다.
	if len(e.committedBlocks) > 0 {
		prevHash = e.committedBlocks[len(e.committedBlocks)-1].Hash
	}

	// 트랜잭션 변환
	// ABCI 앱에서 받은 raw bytes를 Transaction 구조체로 변환한다고 보면된다.
	transactions := make([]types.Transaction, len(preparedTxs))
	for i, txBytes := range preparedTxs {
		transactions[i] = types.Transaction{
			ID:        fmt.Sprintf("tx-%d-%d", seqNum, i),
			Data:      txBytes,
			Timestamp: time.Now(),
		}
	}

	// 새로운 블록 생성함
	block := types.NewBlock(seqNum, prevHash, e.config.NodeID, view, transactions)

	// PrePrepare 메시지 생성 및 저장
	prePrepareMsg := NewPrePrepareMsg(view, seqNum, block, e.config.NodeID)

	state := e.stateLog.GetState(view, seqNum)
	state.SetPrePrepare(prePrepareMsg, block)

	// 페이로드 만들라고 마샬링
	payload, _ := json.Marshal(prePrepareMsg)
	// 새로운 메시지 말들어서
	msg := NewMessage(PrePrepare, view, seqNum, block.Hash, e.config.NodeID)
	// 페이로드에 넣고 
	msg.Payload = payload

	// 브로드 캐스트
	e.broadcast(msg)

	e.logger.Printf("[PBFT-V2] Primary broadcast PRE-PREPARE for seq %d with %d txs", seqNum, len(transactions))

	if e.metrics != nil {
		e.metrics.StartConsensusRound(seqNum)
	}
}

// handlePrePrepare - PrePrepare 메시지 처리 (ABCI ProcessProposal 사용)
func (e *EngineV2) handlePrePrepare(msg *Message) {
	// 1. 리더 확인 -> 리더가 보낸건지
	if msg.NodeID != e.getPrimaryID() {
		e.logger.Printf("[PBFT-V2] Received PRE-PREPARE from non-primary %s", msg.NodeID)
		return
	}

	// 읽기 락
	e.mu.RLock()
	currentView := e.view
	e.mu.RUnlock()

	// 2. 뷰 확인 -> 현재 뷰가 몇 인지
	if msg.View != currentView {
		return
	}

	// 3. 컨텍스트 윈도우 확인
	if !e.stateLog.IsInWindow(msg.SequenceNum) {
		return
	}

	// 4. 메시지 디코딩
	var prePrepareMsg PrePrepareMsg
	if err := json.Unmarshal(msg.Payload, &prePrepareMsg); err != nil {
		e.logger.Printf("[PBFT-V2] Failed to decode PRE-PREPARE: %v", err)
		return
	}

	// 5. ABCI ProcessProposal 호출 - 블록 검증 5초 컨텍스트 정의한거임
	ctx, cancel := context.WithTimeout(e.ctx, 5*time.Second)
	// 끝날 때 cancel
	defer cancel()

	// 6. ABCI 로 부터온 트랜잭션 형식을 엔진에 맞게 바꾼다. 왜나하면 engine -> engine으로 전파될거니까
	// prePrepareMsg.Block.Transactions는 []types.Transaction 타입인데, ABCI의 ProcessProposal은 [][]받기 때무에 변환이 필요한거임
	// 그리고 [][]byte에 대한 설명은 이거 자체가 2차원 바이트 배열이다. 
	// txs 라는 트랜잭션들을 담는 배열이 있을 테고 
	// txs[0] = []byte{0x01, 0x02, 0x03} 
	// txs[1] = []byte{0x04, 0x05}
	// txs[2] = []byte{0x06, 0x07, 0x08}
	// 이런식으로 되어있는 것이다.
	// 이렇게 하면 각 트랜잭션의 구조를 모르고도 raw 바이트로 주고 받을 수 있어서 이렇게 한다.
	txs := make([][]byte, len(prePrepareMsg.Block.Transactions))
	for i, tx := range prePrepareMsg.Block.Transactions {
		txs[i] = tx.Data
	}

	// 제안자도 byte 형태로 만듬
	proposer := []byte(prePrepareMsg.PrimaryID)
	// 앱에 요청 이게 이름이 좀 헷갈리는데 리더로 부터 prepropared 메시지 받으면 그거 검증할라고 앱단에 보내는 함수임.
	accepted, err := e.abciAdapter.ProcessProposal(
		ctx,
		int64(msg.SequenceNum),
		proposer,
		txs,
		msg.Digest,
	)
	
	if err != nil {
		e.logger.Printf("[PBFT-V2] ProcessProposal error: %v", err)
		return
	}

	if !accepted {
		e.logger.Printf("[PBFT-V2] ProcessProposal REJECTED block at seq %d", msg.SequenceNum)
		return
	}

	// StateLog에 저장 엔진의 상태를 업데이트 왜냐? 성공적으로 보내고 검증했으니까
	state := e.stateLog.GetState(msg.View, msg.SequenceNum)
	state.SetPrePrepare(&prePrepareMsg, prePrepareMsg.Block)

	// Prepare 메시지 브로드캐스트
	prepareMsg := NewPrepareMsg(msg.View, msg.SequenceNum, msg.Digest, e.config.NodeID)
	payload, _ := json.Marshal(prepareMsg)
	prepareNetMsg := NewMessage(Prepare, msg.View, msg.SequenceNum, msg.Digest, e.config.NodeID)
	prepareNetMsg.Payload = payload

	e.broadcast(prepareNetMsg)
	e.resetViewChangeTimer()

	e.logger.Printf("[PBFT-V2] Node %s sent PREPARE for seq %d (ProcessProposal ACCEPTED)", e.config.NodeID, msg.SequenceNum)
}

// handlePrepare - Prepare 메시지 처리
func (e *EngineV2) handlePrepare(msg *Message) {
	e.mu.RLock()
	currentView := e.view
	e.mu.RUnlock()

	if msg.View != currentView {
		return
	}

	var prepareMsg PrepareMsg
	if err := json.Unmarshal(msg.Payload, &prepareMsg); err != nil {
		e.logger.Printf("[PBFT-V2] Failed to decode PREPARE: %v", err)
		return
	}

	state := e.stateLog.GetExistingState(msg.SequenceNum)
	if state == nil {
		return
	}

	state.AddPrepare(&prepareMsg)

	quorum := e.validatorSet.QuorumSize()
	if state.IsPrepared(quorum) && state.GetPhase() == PrePrepared {
		state.TransitionToPrepared()

		commitMsg := NewCommitMsg(msg.View, msg.SequenceNum, msg.Digest, e.config.NodeID)
		payload, _ := json.Marshal(commitMsg)
		commitNetMsg := NewMessage(Commit, msg.View, msg.SequenceNum, msg.Digest, e.config.NodeID)
		commitNetMsg.Payload = payload

		e.broadcast(commitNetMsg)

		e.logger.Printf("[PBFT-V2] Node %s PREPARED and sent COMMIT for seq %d (prepares: %d)",
			e.config.NodeID, msg.SequenceNum, state.PrepareCount())
	}
}

// handleCommit - Commit 메시지 처리
func (e *EngineV2) handleCommit(msg *Message) {
	e.mu.RLock()
	currentView := e.view
	e.mu.RUnlock()

	if msg.View != currentView {
		return
	}

	var commitMsg CommitMsg
	if err := json.Unmarshal(msg.Payload, &commitMsg); err != nil {
		e.logger.Printf("[PBFT-V2] Failed to decode COMMIT: %v", err)
		return
	}

	state := e.stateLog.GetExistingState(msg.SequenceNum)
	if state == nil {
		return
	}

	state.AddCommit(&commitMsg)

	quorum := e.validatorSet.QuorumSize()
	if state.IsCommitted(quorum) && state.GetPhase() == Prepared {
		state.TransitionToCommitted()
		e.executeBlock(state)

		e.logger.Printf("[PBFT-V2] Node %s COMMITTED seq %d (commits: %d)",
			e.config.NodeID, msg.SequenceNum, state.CommitCount())
	}
}

// executeBlock - 블록 실행 (ABCI FinalizeBlock + Commit 사용)
func (e *EngineV2) executeBlock(state *State) {
	if state.Executed || state.Block == nil {
		return
	}

	startTime := time.Now()
	ctx, cancel := context.WithTimeout(e.ctx, 30*time.Second)
	defer cancel()

	// ABCI FinalizeBlock 호출
	result, err := e.abciAdapter.FinalizeBlock(ctx, state.Block)
	if err != nil {
		e.logger.Printf("[PBFT-V2] FinalizeBlock failed: %v", err)
		return
	}

	// 트랜잭션 결과 확인
	failedCount := 0
	for i, txResult := range result.TxResults {
		if txResult.Code != 0 {
			e.logger.Printf("[PBFT-V2] Tx %d failed (code=%d): %s", i, txResult.Code, txResult.Log)
			failedCount++
		}
	}
	if failedCount > 0 {
		e.logger.Printf("[PBFT-V2] %d/%d transactions failed in block %d",
			failedCount, len(result.TxResults), state.SequenceNum)
	}

	// ABCI Commit 호출
	appHash, retainHeight, err := e.abciAdapter.Commit(ctx)
	if err != nil {
		e.logger.Printf("[PBFT-V2] Commit failed: %v", err)
		return
	}

	// 앱 해시 저장
	e.mu.Lock()
	e.lastAppHash = result.AppHash
	e.abciAdapter.SetLastAppHash(result.AppHash)
	e.mu.Unlock()

	// 실행 완료 표시
	state.MarkExecuted()

	// 확정된 블록 목록에 추가
	e.mu.Lock()
	e.committedBlocks = append(e.committedBlocks, state.Block)
	mp := e.mempool
	e.mu.Unlock()

	// Mempool에서 커밋된 트랜잭션 제거
	if mp != nil && len(state.Block.Transactions) > 0 {
		committedTxs := make([][]byte, len(state.Block.Transactions))
		for i, tx := range state.Block.Transactions {
			committedTxs[i] = tx.Data
		}
		mp.Update(int64(state.SequenceNum), committedTxs)
		e.logger.Printf("[PBFT-V2] Mempool updated: removed %d committed txs, remaining: %d",
			len(committedTxs), mp.Size())
	}

	// 메트릭 기록
	if e.metrics != nil {
		e.metrics.EndConsensusRound(state.SequenceNum)
		e.metrics.SetBlockHeight(state.SequenceNum)
		e.metrics.RecordBlockExecutionTime(time.Since(startTime))
		e.metrics.AddTransactions(len(state.Block.Transactions))
	}

	e.logger.Printf("[PBFT-V2] Executed block at height %d with %d txs, appHash=%x, retainHeight=%d",
		state.SequenceNum, len(state.Block.Transactions), result.AppHash, retainHeight)

	// 검증자 업데이트 처리
	if len(result.ValidatorUpdates) > 0 {
		e.handleValidatorUpdates(result.ValidatorUpdates)
	}

	// 체크포인트 생성
	if state.SequenceNum%e.config.CheckpointInterval == 0 {
		e.createCheckpoint(state.SequenceNum)
	}

	_ = appHash // 사용되지 않는 변수 경고 방지
}

// handleValidatorUpdates - 검증자 업데이트 처리
func (e *EngineV2) handleValidatorUpdates(updates []abci.ValidatorUpdate) {
	e.mu.Lock()
	defer e.mu.Unlock()

	for _, update := range updates {
		// CometBFT v0.38.x: PublicKey는 oneof로 Ed25519 또는 Secp256k1 키를 포함
		var pubKey []byte
		if ed25519Key := update.PubKey.GetEd25519(); ed25519Key != nil {
			pubKey = ed25519Key
		} else if secp256k1Key := update.PubKey.GetSecp256K1(); secp256k1Key != nil {
			pubKey = secp256k1Key
		}
		if pubKey == nil {
			continue
		}

		pubKeyStr := string(pubKey)
		if update.Power == 0 {
			// 검증자 제거
			e.removeValidatorByPubKey(pubKey)
			e.logger.Printf("[PBFT-V2] Validator removed: %x...", pubKey[:min(16, len(pubKey))])
		} else {
			// 검증자 추가/업데이트
			e.updateOrAddValidator(&types.Validator{
				ID:        pubKeyStr,
				PublicKey: pubKey,
				Power:     update.Power,
			})
			e.logger.Printf("[PBFT-V2] Validator updated: %x..., power=%d",
				pubKey[:min(16, len(pubKey))], update.Power)
		}
	}

	// ViewChangeManager quorum 업데이트
	e.updateViewChangeQuorum()
}

// removeValidatorByPubKey - 공개키로 검증자 제거 (내부 헬퍼)
func (e *EngineV2) removeValidatorByPubKey(pubKey []byte) {
	newValidators := make([]*types.Validator, 0)
	for _, v := range e.validatorSet.Validators {
		if string(v.PublicKey) != string(pubKey) {
			newValidators = append(newValidators, v)
		}
	}
	e.validatorSet.Validators = newValidators
}

// updateOrAddValidator - 검증자 추가 또는 업데이트 (내부 헬퍼)
func (e *EngineV2) updateOrAddValidator(v *types.Validator) {
	for i, existing := range e.validatorSet.Validators {
		if existing.ID == v.ID {
			e.validatorSet.Validators[i] = v
			return
		}
	}
	e.validatorSet.Validators = append(e.validatorSet.Validators, v)
}

// updateViewChangeQuorum - 뷰 체인지 쿼럼 업데이트 (내부 헬퍼)
func (e *EngineV2) updateViewChangeQuorum() {
	if e.viewChangeManager != nil {
		e.viewChangeManager.quorumSize = e.validatorSet.QuorumSize()
	}
}

// createCheckpoint - 체크포인트 생성
func (e *EngineV2) createCheckpoint(seqNum uint64) {
	e.mu.Lock()
	if len(e.committedBlocks) > 0 {
		lastBlock := e.committedBlocks[len(e.committedBlocks)-1]
		e.checkpoints[seqNum] = lastBlock.Hash
	}

	// 오래된 체크포인트 정리
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

	e.stateLog.AdvanceWatermarks(seqNum)
	e.logger.Printf("[PBFT-V2] Created checkpoint at seq %d", seqNum)
}

// View Change 관련 메서드들 (기존과 동일)

func (e *EngineV2) handleViewChange(msg *Message) {
	e.logger.Printf("[PBFT-V2] Received VIEW-CHANGE from %s for view %d", msg.NodeID, msg.View)

	var viewChangeMsg ViewChangeMsg
	if err := json.Unmarshal(msg.Payload, &viewChangeMsg); err != nil {
		e.logger.Printf("[PBFT-V2] Failed to decode VIEW-CHANGE: %v", err)
		return
	}

	e.mu.RLock()
	currentView := e.view
	e.mu.RUnlock()

	if viewChangeMsg.NewView <= currentView {
		return
	}

	hasQuorum := e.viewChangeManager.HandleViewChange(&viewChangeMsg)

	if hasQuorum {
		e.logger.Printf("[PBFT-V2] Got quorum for view %d", viewChangeMsg.NewView)

		newPrimaryIdx := int(viewChangeMsg.NewView) % len(e.validatorSet.Validators)
		newPrimaryID := e.validatorSet.Validators[newPrimaryIdx].ID

		if newPrimaryID == e.config.NodeID {
			e.broadcastNewView(viewChangeMsg.NewView)
		}
	}
}

func (e *EngineV2) handleNewView(msg *Message) {
	e.logger.Printf("[PBFT-V2] Received NEW-VIEW from %s for view %d", msg.NodeID, msg.View)

	var newViewMsg NewViewMsg
	if err := json.Unmarshal(msg.Payload, &newViewMsg); err != nil {
		e.logger.Printf("[PBFT-V2] Failed to decode NEW-VIEW: %v", err)
		return
	}

	e.mu.RLock()
	currentView := e.view
	e.mu.RUnlock()

	if newViewMsg.View <= currentView {
		return
	}

	expectedPrimaryIdx := int(newViewMsg.View) % len(e.validatorSet.Validators)
	expectedPrimaryID := e.validatorSet.Validators[expectedPrimaryIdx].ID

	if newViewMsg.NewPrimaryID != expectedPrimaryID {
		e.logger.Printf("[PBFT-V2] NEW-VIEW from wrong primary: got %s, expected %s",
			newViewMsg.NewPrimaryID, expectedPrimaryID)
		return
	}

	if e.viewChangeManager.HandleNewView(&newViewMsg) {
		e.logger.Printf("[PBFT-V2] NEW-VIEW accepted for view %d", newViewMsg.View)

		for _, prePrepare := range newViewMsg.PrePrepareMsgs {
			e.reprocessPrePrepare(&prePrepare, newViewMsg.View)
		}
	}
}

func (e *EngineV2) reprocessPrePrepare(prePrepare *PrePrepareMsg, newView uint64) {
	prePrepare.View = newView

	state := e.stateLog.GetState(newView, prePrepare.SequenceNum)
	state.SetPrePrepare(prePrepare, prePrepare.Block)

	prepareMsg := NewPrepareMsg(newView, prePrepare.SequenceNum, prePrepare.Digest, e.config.NodeID)
	payload, _ := json.Marshal(prepareMsg)
	prepareNetMsg := NewMessage(Prepare, newView, prePrepare.SequenceNum, prePrepare.Digest, e.config.NodeID)
	prepareNetMsg.Payload = payload

	e.broadcast(prepareNetMsg)

	e.logger.Printf("[PBFT-V2] Reprocessed PRE-PREPARE for seq %d in new view %d",
		prePrepare.SequenceNum, newView)
}

func (e *EngineV2) broadcastNewView(newView uint64) {
	newViewMsg := e.viewChangeManager.CreateNewViewMsg(newView, len(e.validatorSet.Validators))
	if newViewMsg == nil {
		e.logger.Printf("[PBFT-V2] Failed to create NEW-VIEW message")
		return
	}

	payload, _ := json.Marshal(newViewMsg)
	msg := NewMessage(NewView, newView, e.sequenceNum, nil, e.config.NodeID)
	msg.Payload = payload

	e.broadcast(msg)
	e.viewChangeManager.HandleNewView(newViewMsg)

	e.logger.Printf("[PBFT-V2] Broadcast NEW-VIEW for view %d", newView)
}

func (e *EngineV2) startViewChange() {
	e.mu.Lock()
	newView := e.view + 1
	lastSeqNum := e.sequenceNum
	e.mu.Unlock()

	e.logger.Printf("[PBFT-V2] Starting view change to view %d (timeout)", newView)

	if e.metrics != nil {
		e.metrics.IncrementViewChanges()
	}

	checkpoints := e.collectCheckpoints()
	preparedSet := e.collectPreparedCertificates()

	e.viewChangeManager.StartViewChange(newView, lastSeqNum, checkpoints, preparedSet)

	e.mu.Lock()
	e.viewChangeTimer = time.AfterFunc(e.config.ViewChangeTimeout*2, func() {
		e.startViewChange()
	})
	e.mu.Unlock()
}

func (e *EngineV2) collectCheckpoints() []Checkpoint {
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

func (e *EngineV2) collectPreparedCertificates() []PreparedCert {
	quorum := e.validatorSet.QuorumSize()
	var preparedCerts []PreparedCert

	e.mu.RLock()
	view := e.view
	e.mu.RUnlock()

	for seqNum := e.stateLog.LowWaterMark + 1; seqNum <= e.stateLog.HighWaterMark; seqNum++ {
		state := e.stateLog.GetExistingState(seqNum)
		if state == nil {
			continue
		}

		if state.IsPrepared(quorum) && !state.Executed && state.PrePrepareMsg != nil {
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

func (e *EngineV2) onViewChangeComplete(newView uint64) {
	e.mu.Lock()
	e.view = newView
	e.mu.Unlock()

	e.logger.Printf("[PBFT-V2] View change completed. New view: %d", newView)

	if e.metrics != nil {
		e.metrics.SetCurrentView(newView)
	}

	e.resetViewChangeTimer()

	if e.isPrimary() {
		e.logger.Printf("[PBFT-V2] I am the new primary for view %d", newView)
	}
}

func (e *EngineV2) resetViewChangeTimer() {
	if e.viewChangeTimer != nil {
		e.viewChangeTimer.Stop()
	}
	e.viewChangeTimer = time.AfterFunc(e.config.ViewChangeTimeout, func() {
		e.startViewChange()
	})
}

func (e *EngineV2) broadcast(msg *Message) {
	if e.transport != nil {
		if err := e.transport.Broadcast(msg); err != nil {
			e.logger.Printf("[PBFT-V2] Broadcast failed: %v", err)
		}
	}

	if e.metrics != nil {
		e.metrics.IncrementMessagesSent(msg.Type.String())
	}
}

// 공개 API 메서드들

// SetMempool - Mempool 설정 (Node에서 호출)
func (e *EngineV2) SetMempool(mp *mempool.Mempool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.mempool = mp
	e.logger.Printf("[PBFT-V2] Mempool connected to engine")
}

// GetMempool - Mempool 반환
func (e *EngineV2) GetMempool() *mempool.Mempool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.mempool
}

// SubmitRequest - 트랜잭션 제출
func (e *EngineV2) SubmitRequest(operation []byte, clientID string) error {
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

// GetCurrentView - 현재 뷰 반환
func (e *EngineV2) GetCurrentView() uint64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.view
}

// GetCurrentHeight - 현재 블록 높이 반환
func (e *EngineV2) GetCurrentHeight() uint64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.sequenceNum
}

// GetCommittedBlocks - 확정된 블록 목록 반환
func (e *EngineV2) GetCommittedBlocks() []*types.Block {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.committedBlocks
}

// GetLastAppHash - 마지막 앱 해시 반환
func (e *EngineV2) GetLastAppHash() []byte {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.lastAppHash
}

// IsPrimary - 현재 노드가 리더인지 확인 (공개 메서드)
func (e *EngineV2) IsPrimary() bool {
	return e.isPrimary()
}

// GetPrimaryID - 현재 리더 ID 반환 (공개 메서드)
func (e *EngineV2) GetPrimaryID() string {
	return e.getPrimaryID()
}

// min - 최소값 헬퍼 함수
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// StateProvider 인터페이스 구현 - ConsensusEngine 인터페이스 충족을 위해 추가

// GetBlocksFromHeight returns blocks from the specified height.
// 지정된 높이부터 커밋된 블록들을 반환합니다.
func (e *EngineV2) GetBlocksFromHeight(fromHeight uint64) []*types.Block {
	e.mu.RLock()
	defer e.mu.RUnlock()

	var result []*types.Block
	for _, block := range e.committedBlocks {
		if block.Header.Height >= fromHeight {
			result = append(result, block)
		}
	}
	return result
}

// GetCheckpoints returns all stored checkpoints.
// 저장된 모든 체크포인트를 반환합니다.
func (e *EngineV2) GetCheckpoints() []Checkpoint {
	e.mu.RLock()
	defer e.mu.RUnlock()

	checkpoints := make([]Checkpoint, 0, len(e.checkpoints))
	for seqNum, digest := range e.checkpoints {
		checkpoints = append(checkpoints, Checkpoint{
			SequenceNum: seqNum,
			Digest:      digest,
			NodeID:      e.config.NodeID,
		})
	}
	return checkpoints
}

// GetCheckpoint returns the checkpoint for a specific sequence number.
// 특정 시퀀스 번호의 체크포인트를 반환합니다.
func (e *EngineV2) GetCheckpoint(seqNum uint64) (*Checkpoint, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	digest, exists := e.checkpoints[seqNum]
	if !exists {
		return nil, false
	}

	return &Checkpoint{
		SequenceNum: seqNum,
		Digest:      digest,
		NodeID:      e.config.NodeID,
	}, true
}
