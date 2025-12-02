// Package abci provides ABCI 2.0 type conversions for CometBFT v0.38.x
package abci

import (
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
)

// ExecutionResult - 블록 실행 결과
type ExecutionResult struct {
	TxResults             []TxResult // 트랜잭션 결과들
	ValidatorUpdates      []abci.ValidatorUpdate // 검증자
	ConsensusParamUpdates *abci.ConsensusParams // 합의 파라미터
	AppHash               []byte // 앱 해시
	Events                []abci.Event // 이벤트들
} 

// TxResult - 트랜잭션 실행 결과
type TxResult struct {
	Code      uint32 // 트랜잭션 성공 실패
	Data      []byte // 반환 데이터
	Log       string // 로그 메시지
	Info      string // 추가 정보
	GasWanted int64 // 요청 가스
	GasUsed   int64 // 사용 가스
	Events    []abci.Event // 이벤트들
}

// QueryResult - 쿼리 결과
type QueryResult struct {
	Key      []byte // 키
	Value    []byte // 값
	Height   int64 // 높이
	ProofOps *abci.ProofOps // 증명 정보
}

// BlockData - PBFT 블록 데이터 (ABCI 변환용)
type BlockData struct {
	Height       int64 // 블록 높이
	Txs          [][]byte // 트랜잭션들
	Hash         []byte // 블록 해시
	Time         time.Time // 타임스탬프
	ProposerAddr []byte // 제안자 주소
}

// NewPrepareProposalRequest - PrepareProposal 요청 생성
func NewPrepareProposalRequest(
	txs [][]byte,
	maxTxBytes int64,
	height int64,
	t time.Time,
	proposer []byte,
) *abci.RequestPrepareProposal {
	return &abci.RequestPrepareProposal{
		Txs:             txs,
		MaxTxBytes:      maxTxBytes,
		Height:          height,
		Time:            t,
		ProposerAddress: proposer,
		LocalLastCommit: abci.ExtendedCommitInfo{},
		Misbehavior:     []abci.Misbehavior{},
	}
}

// NewProcessProposalRequest - ProcessProposal 요청 생성
func NewProcessProposalRequest(
	txs [][]byte,
	height int64,
	t time.Time,
	proposer []byte,
	hash []byte,
) *abci.RequestProcessProposal {
	return &abci.RequestProcessProposal{
		Txs:                txs,
		Hash:               hash,
		Height:             height,
		Time:               t,
		ProposerAddress:    proposer,
		ProposedLastCommit: abci.CommitInfo{},
		Misbehavior:        []abci.Misbehavior{},
	}
}

// NewFinalizeBlockRequest - FinalizeBlock 요청 생성
func NewFinalizeBlockRequest(block *BlockData) *abci.RequestFinalizeBlock {
	return &abci.RequestFinalizeBlock{
		Txs:               block.Txs,
		Hash:              block.Hash,
		Height:            block.Height,
		Time:              block.Time,
		ProposerAddress:   block.ProposerAddr,
		DecidedLastCommit: abci.CommitInfo{},
		Misbehavior:       []abci.Misbehavior{},
	}
}

// FinalizeBlockResponseToResult - ABCI ResponseFinalizeBlock → ExecutionResult 변환
func FinalizeBlockResponseToResult(resp *abci.ResponseFinalizeBlock) *ExecutionResult {
	txResults := make([]TxResult, len(resp.TxResults))
	for i, r := range resp.TxResults {
		txResults[i] = TxResult{
			Code:      r.Code,
			Data:      r.Data,
			Log:       r.Log,
			Info:      r.Info,
			GasWanted: r.GasWanted,
			GasUsed:   r.GasUsed,
			Events:    r.Events,
		}
	}

	return &ExecutionResult{
		TxResults:             txResults,
		ValidatorUpdates:      resp.ValidatorUpdates,
		ConsensusParamUpdates: resp.ConsensusParamUpdates,
		AppHash:               resp.AppHash,
		Events:                resp.Events,
	}
}

// ValidatorUpdate - 검증자 업데이트 헬퍼
type ValidatorUpdate struct {
	PubKey []byte // 공개키
	Power  int64 // 투표력
}

// ToABCIValidatorUpdate - ABCI ValidatorUpdate로 변환
func (v *ValidatorUpdate) ToABCIValidatorUpdate() abci.ValidatorUpdate {
	return abci.ValidatorUpdate{
		PubKey: abci.PubKey{
			Type: "ed25519",
			Data: v.PubKey,
		},
		Power: v.Power,
	}
}

// FromABCIValidatorUpdate - ABCI ValidatorUpdate에서 변환
func FromABCIValidatorUpdate(update abci.ValidatorUpdate) *ValidatorUpdate {
	return &ValidatorUpdate{
		PubKey: update.PubKey.Data,
		Power:  update.Power,
	}
}
