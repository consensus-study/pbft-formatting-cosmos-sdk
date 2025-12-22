// Package transport provides gRPC-based P2P networking for PBFT consensus.
package transport

import (
	"encoding/json"
	"fmt"

	"google.golang.org/grpc/encoding"
)

func init() {
	// JSON 코덱 등록 - proto.Message를 구현하지 않은 타입도 지원
	encoding.RegisterCodec(JSONCodec{})
}

// JSONCodec은 gRPC에서 JSON 직렬화를 사용하는 코덱
// 수동으로 정의한 Go struct들을 gRPC로 전송할 수 있게 해줌
type JSONCodec struct{}

// Name returns the name of the codec
func (JSONCodec) Name() string {
	return "proto" // proto 이름을 사용해서 기본 코덱을 대체
}

// Marshal serializes the message to JSON
func (JSONCodec) Marshal(v interface{}) ([]byte, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("json marshal error: %w", err)
	}
	return data, nil
}

// Unmarshal deserializes the message from JSON
func (JSONCodec) Unmarshal(data []byte, v interface{}) error {
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("json unmarshal error: %w", err)
	}
	return nil
}
