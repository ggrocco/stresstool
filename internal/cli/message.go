package cli

import (
	"stresstool/internal/protocol"
)

func newProtocolMessage(msgType protocol.MessageType, payload any) (protocol.Message, error) {
	return protocol.NewMessage(msgType, payload)
}

func decodeProtocolMessageData[T any](msg protocol.Message) (*T, error) {
	var out T
	if err := msg.DecodeData(&out); err != nil {
		return nil, err
	}
	return &out, nil
}
