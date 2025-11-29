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
/**
 *- newView: 바꾸려는 새 뷰 번호 (예: 1)
- lastSeqNum: 내가 처리한 마지막 블록 번호 (예: 305)
- checkpoints: 내가 가진 체크포인트들 (예: [{100}, {200}, {300}])
- preparedSet: 내가 Prepared 상태인 블록들 (예: [블록301, 블록302])
*/
// 바뀐 View로 브로드 캐스트

func (vcm *ViewChangeManager) StartViewChange(newView uint64, lastSeqNum uint64, checkpoints []Checkpoint, preparedSet []PreparedCert) {
	vcm.mu.Lock()
	defer vcm.mu.Unlock()

	if newView <= vcm.currentView {
		return
	}

	vcm.inProgress = true

	// 뷰 변경 메시지 생성
	// 포인터로 생성
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

// 새로운 제안자 체인지 메서드
// 다른 노드가 보낸 ViewChange 메시지를 처리하는 함수 
// msg 다른 노드가 보낸 ViewChange 메시지를 처리하는 함수
// return -> true면 투표가 충분히 된 것이고, false면 아직 부족한 것이다.
func (vcm *ViewChangeManager) HandleViewChange(msg *ViewChangeMsg) bool {
	// 쓰기 락
	vcm.mu.Lock()
	defer vcm.mu.Unlock()

	// nil 이면 맵 초기화
	if vcm.viewChangeMsgs[msg.NewView] == nil {
		vcm.viewChangeMsgs[msg.NewView] = make(map[string]*ViewChangeMsg)
	}

	// 메시지 저장 (투표)
	vcm.viewChangeMsgs[msg.NewView][msg.NodeID] = msg

	// 충분한 투표 있으면 true 리턴
	if len(vcm.viewChangeMsgs[msg.NewView]) >= vcm.quorumSize {
		return true
	}

	return false
}

// 새 프라이머리 호출
// 새 리더가 호출하는 함수 
// ViewChange 투표들을 모아서 NewView 메세지 생성
// 정확히는 리더가 되기 위해 호출하는 함수이다.
func (vcm *ViewChangeManager) CreateNewViewMsg(newView uint64, validatorCount int) *NewViewMsg {
	// 읽기 락
	vcm.mu.RLock()
	defer vcm.mu.RUnlock()

	// 쿼럼 체크 비탄진 만족?
	viewChangeMsgs := vcm.viewChangeMsgs[newView]
	if len(viewChangeMsgs) < vcm.quorumSize {
		return nil
	}

	// 뷰 체인지 메서드
	var vcMsgs []ViewChangeMsg
	for _, msg := range viewChangeMsgs {
		vcMsgs = append(vcMsgs, *msg)
	}

	// 이어서 처리할 블록들 계산
	prePrepares := vcm.computePrePrepareSet(vcMsgs)

	// NewView 메시지 생성
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
	// 가장 높은 시퀸스 찾음
	// Go에서는 자동으로 0으로 초기화
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

// 새 리더가 보낸 NewViewMsg를 처리하는 함수
func (vcm *ViewChangeManager) HandleNewView(msg *NewViewMsg) bool {
	vcm.mu.Lock()
	defer vcm.mu.Unlock()

	// NewViewMsg가 정당한지 검증
	// ViewChangeMsgs가 2f + 1개 이상인가?
	// 모든 ViewChnageMsgs가 같은 뷰에 대한 건가?
	if !vcm.verifyNewViewMsg(msg) {
		return false
	}

	// 메시지 저장
	vcm.newViewMsgs[msg.View] = msg

	// 현재 뷰를 새 뷰로 변경
	// 이제 공식적으로 View 1
	vcm.currentView = msg.View
	// 뷰 체인이 진행 중 플래그 해제
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

// 새 리더가 보낸 NewViewMsg가 정당한지 검증하는 함수
func (vcm *ViewChangeManager) verifyNewViewMsg(msg *NewViewMsg) bool {
	//쿼럼 즉 NewViewMsg안에 ViewChange 투표가 충분히 들어있는지 확인
	if len(msg.ViewChangeMsgs) < vcm.quorumSize {
		return false
	}

	// 모든 ViewChange 메시지가 같은 뷰에 대한 건지 확인
	for _, vcMsg := range msg.ViewChangeMsgs {
		if vcMsg.NewView != msg.View {
			return false
		}
	}

	// PrePrepare 집합이 올바르게 계산되었는지 검증
	// 1. ViewChange 메시지들에서 가장 높은 체크포인트 찾기
	var maxCheckpoint uint64
	for _, vcMsg := range msg.ViewChangeMsgs {
		for _, cp := range vcMsg.Checkpoints {
			if cp.SequenceNum > maxCheckpoint {
				maxCheckpoint = cp.SequenceNum
			}
		}
	}

	// 2. ViewChange 메시지들에서 Prepared 블록들 수집
	preparedBlocks := make(map[uint64][]byte) // seqNum -> digest
	var maxPrepared uint64

	for _, vcMsg := range msg.ViewChangeMsgs {
		for _, pc := range vcMsg.PreparedSet {
			seqNum := pc.PrePrepare.SequenceNum
			if seqNum > maxPrepared {
				maxPrepared = seqNum
			}
			// 같은 시퀀스에 대해 다른 다이제스트가 있으면 안됨 (Safety)
			if existingDigest, exists := preparedBlocks[seqNum]; exists {
				if !bytesEqual(existingDigest, pc.PrePrepare.Digest) {
					// 충돌 발견 - 비잔틴 노드 존재 가능
					return false
				}
			} else {
				preparedBlocks[seqNum] = pc.PrePrepare.Digest
			}
		}
	}

	// 3. NewView의 PrePrepare들이 올바른지 확인
	for _, pp := range msg.PrePrepareMsgs {
		// 체크포인트 이하의 시퀀스는 이미 커밋됨 -> 포함되면 안됨
		if pp.SequenceNum <= maxCheckpoint {
			return false
		}

		// Prepared 블록이 있으면 같은 다이제스트여야 함
		if expectedDigest, exists := preparedBlocks[pp.SequenceNum]; exists {
			if !bytesEqual(expectedDigest, pp.Digest) {
				return false
			}
		}
	}

	// 4. maxCheckpoint+1 ~ maxPrepared 사이의 모든 Prepared 블록이 포함되어야 함
	for seqNum := maxCheckpoint + 1; seqNum <= maxPrepared; seqNum++ {
		if _, hasPrepared := preparedBlocks[seqNum]; hasPrepared {
			// 이 시퀀스에 대한 PrePrepare가 NewView에 있어야 함
			found := false
			for _, pp := range msg.PrePrepareMsgs {
				if pp.SequenceNum == seqNum {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
	}

	return true
}

// bytesEqual - 두 바이트 슬라이스 비교
func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
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
