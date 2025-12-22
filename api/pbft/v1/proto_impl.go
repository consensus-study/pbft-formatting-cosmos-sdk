// proto_impl.go - protobuf Message 인터페이스 구현
// gRPC에서 사용하기 위해 proto.Message 인터페이스를 구현
package pbftv1

import (
	"fmt"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// ================================================================================
//                          PBFTMessage proto.Message 구현
// ================================================================================

var _ proto.Message = (*PBFTMessage)(nil)

func (*PBFTMessage) ProtoMessage() {}

func (x *PBFTMessage) Reset() {
	*x = PBFTMessage{}
}

func (x *PBFTMessage) String() string {
	return fmt.Sprintf("PBFTMessage{Type:%v, View:%d, Seq:%d}", x.Type, x.View, x.SequenceNum)
}

func (*PBFTMessage) ProtoReflect() protoreflect.Message {
	return nil // 최소 구현
}

// ================================================================================
//                          BroadcastMessageRequest proto.Message 구현
// ================================================================================

var _ proto.Message = (*BroadcastMessageRequest)(nil)

func (*BroadcastMessageRequest) ProtoMessage() {}

func (x *BroadcastMessageRequest) Reset() {
	*x = BroadcastMessageRequest{}
}

func (x *BroadcastMessageRequest) String() string {
	return "BroadcastMessageRequest"
}

func (*BroadcastMessageRequest) ProtoReflect() protoreflect.Message {
	return nil
}

// ================================================================================
//                          BroadcastMessageResponse proto.Message 구현
// ================================================================================

var _ proto.Message = (*BroadcastMessageResponse)(nil)

func (*BroadcastMessageResponse) ProtoMessage() {}

func (x *BroadcastMessageResponse) Reset() {
	*x = BroadcastMessageResponse{}
}

func (x *BroadcastMessageResponse) String() string {
	return fmt.Sprintf("BroadcastMessageResponse{Success:%v}", x.Success)
}

func (*BroadcastMessageResponse) ProtoReflect() protoreflect.Message {
	return nil
}

// ================================================================================
//                          SendMessageRequest proto.Message 구현
// ================================================================================

var _ proto.Message = (*SendMessageRequest)(nil)

func (*SendMessageRequest) ProtoMessage() {}

func (x *SendMessageRequest) Reset() {
	*x = SendMessageRequest{}
}

func (x *SendMessageRequest) String() string {
	return fmt.Sprintf("SendMessageRequest{Target:%s}", x.TargetNodeId)
}

func (*SendMessageRequest) ProtoReflect() protoreflect.Message {
	return nil
}

// ================================================================================
//                          SendMessageResponse proto.Message 구현
// ================================================================================

var _ proto.Message = (*SendMessageResponse)(nil)

func (*SendMessageResponse) ProtoMessage() {}

func (x *SendMessageResponse) Reset() {
	*x = SendMessageResponse{}
}

func (x *SendMessageResponse) String() string {
	return fmt.Sprintf("SendMessageResponse{Success:%v}", x.Success)
}

func (*SendMessageResponse) ProtoReflect() protoreflect.Message {
	return nil
}

// ================================================================================
//                          SyncStateRequest proto.Message 구현
// ================================================================================

var _ proto.Message = (*SyncStateRequest)(nil)

func (*SyncStateRequest) ProtoMessage() {}

func (x *SyncStateRequest) Reset() {
	*x = SyncStateRequest{}
}

func (x *SyncStateRequest) String() string {
	return fmt.Sprintf("SyncStateRequest{From:%d, To:%d}", x.FromHeight, x.ToHeight)
}

func (*SyncStateRequest) ProtoReflect() protoreflect.Message {
	return nil
}

// ================================================================================
//                          SyncStateResponse proto.Message 구현
// ================================================================================

var _ proto.Message = (*SyncStateResponse)(nil)

func (*SyncStateResponse) ProtoMessage() {}

func (x *SyncStateResponse) Reset() {
	*x = SyncStateResponse{}
}

func (x *SyncStateResponse) String() string {
	return "SyncStateResponse"
}

func (*SyncStateResponse) ProtoReflect() protoreflect.Message {
	return nil
}

// ================================================================================
//                          GetCheckpointRequest proto.Message 구현
// ================================================================================

var _ proto.Message = (*GetCheckpointRequest)(nil)

func (*GetCheckpointRequest) ProtoMessage() {}

func (x *GetCheckpointRequest) Reset() {
	*x = GetCheckpointRequest{}
}

func (x *GetCheckpointRequest) String() string {
	return fmt.Sprintf("GetCheckpointRequest{Seq:%d}", x.SequenceNum)
}

func (*GetCheckpointRequest) ProtoReflect() protoreflect.Message {
	return nil
}

// ================================================================================
//                          GetCheckpointResponse proto.Message 구현
// ================================================================================

var _ proto.Message = (*GetCheckpointResponse)(nil)

func (*GetCheckpointResponse) ProtoMessage() {}

func (x *GetCheckpointResponse) Reset() {
	*x = GetCheckpointResponse{}
}

func (x *GetCheckpointResponse) String() string {
	return "GetCheckpointResponse"
}

func (*GetCheckpointResponse) ProtoReflect() protoreflect.Message {
	return nil
}

// ================================================================================
//                          GetStatusRequest proto.Message 구현
// ================================================================================

var _ proto.Message = (*GetStatusRequest)(nil)

func (*GetStatusRequest) ProtoMessage() {}

func (x *GetStatusRequest) Reset() {
	*x = GetStatusRequest{}
}

func (x *GetStatusRequest) String() string {
	return "GetStatusRequest"
}

func (*GetStatusRequest) ProtoReflect() protoreflect.Message {
	return nil
}

// ================================================================================
//                          GetStatusResponse proto.Message 구현
// ================================================================================

var _ proto.Message = (*GetStatusResponse)(nil)

func (*GetStatusResponse) ProtoMessage() {}

func (x *GetStatusResponse) Reset() {
	*x = GetStatusResponse{}
}

func (x *GetStatusResponse) String() string {
	return fmt.Sprintf("GetStatusResponse{NodeId:%s}", x.NodeId)
}

func (*GetStatusResponse) ProtoReflect() protoreflect.Message {
	return nil
}

// ================================================================================
//                          TxMessage proto.Message 구현
// ================================================================================

var _ proto.Message = (*TxMessage)(nil)

func (*TxMessage) ProtoMessage() {}

func (x *TxMessage) Reset() {
	*x = TxMessage{}
}

func (x *TxMessage) String() string {
	return fmt.Sprintf("TxMessage{Hash:%s}", x.TxHash)
}

func (*TxMessage) ProtoReflect() protoreflect.Message {
	return nil
}

// ================================================================================
//                          BroadcastTxRequest proto.Message 구현
// ================================================================================

var _ proto.Message = (*BroadcastTxRequest)(nil)

func (*BroadcastTxRequest) ProtoMessage() {}

func (x *BroadcastTxRequest) Reset() {
	*x = BroadcastTxRequest{}
}

func (x *BroadcastTxRequest) String() string {
	return "BroadcastTxRequest"
}

func (*BroadcastTxRequest) ProtoReflect() protoreflect.Message {
	return nil
}

// ================================================================================
//                          BroadcastTxResponse proto.Message 구현
// ================================================================================

var _ proto.Message = (*BroadcastTxResponse)(nil)

func (*BroadcastTxResponse) ProtoMessage() {}

func (x *BroadcastTxResponse) Reset() {
	*x = BroadcastTxResponse{}
}

func (x *BroadcastTxResponse) String() string {
	return fmt.Sprintf("BroadcastTxResponse{Success:%v}", x.Success)
}

func (*BroadcastTxResponse) ProtoReflect() protoreflect.Message {
	return nil
}

// ================================================================================
//                          SendTxRequest proto.Message 구현
// ================================================================================

var _ proto.Message = (*SendTxRequest)(nil)

func (*SendTxRequest) ProtoMessage() {}

func (x *SendTxRequest) Reset() {
	*x = SendTxRequest{}
}

func (x *SendTxRequest) String() string {
	return fmt.Sprintf("SendTxRequest{Target:%s}", x.TargetNodeId)
}

func (*SendTxRequest) ProtoReflect() protoreflect.Message {
	return nil
}

// ================================================================================
//                          SendTxResponse proto.Message 구현
// ================================================================================

var _ proto.Message = (*SendTxResponse)(nil)

func (*SendTxResponse) ProtoMessage() {}

func (x *SendTxResponse) Reset() {
	*x = SendTxResponse{}
}

func (x *SendTxResponse) String() string {
	return fmt.Sprintf("SendTxResponse{Success:%v}", x.Success)
}

func (*SendTxResponse) ProtoReflect() protoreflect.Message {
	return nil
}

// ================================================================================
//                          Block proto.Message 구현
// ================================================================================

var _ proto.Message = (*Block)(nil)

func (*Block) ProtoMessage() {}

func (x *Block) Reset() {
	*x = Block{}
}

func (x *Block) String() string {
	return "Block"
}

func (*Block) ProtoReflect() protoreflect.Message {
	return nil
}

// ================================================================================
//                          Checkpoint proto.Message 구현
// ================================================================================

var _ proto.Message = (*Checkpoint)(nil)

func (*Checkpoint) ProtoMessage() {}

func (x *Checkpoint) Reset() {
	*x = Checkpoint{}
}

func (x *Checkpoint) String() string {
	return fmt.Sprintf("Checkpoint{Seq:%d}", x.SequenceNum)
}

func (*Checkpoint) ProtoReflect() protoreflect.Message {
	return nil
}
