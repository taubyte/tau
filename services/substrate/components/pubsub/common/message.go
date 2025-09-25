package common

import (
	"errors"

	"github.com/fxamacker/cbor/v2"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/peer"
	pubsubIface "github.com/taubyte/tau/core/services/substrate/components/pubsub"
)

type message struct {
	from   peer.ID
	topic  string
	Source string `cbor:"1"`
	Data   []byte `cbor:"2"`
}

func NewMessage[T *pubsub.Message | []byte](in T, source string) (pubsubIface.Message, error) {
	switch in := any(in).(type) {
	case *pubsub.Message:
		var msg message
		err := cbor.Unmarshal(in.GetData(), &msg)
		if err != nil {
			return nil, err
		}
		msg.from = in.GetFrom()
		msg.topic = in.GetTopic()
		if source != "" {
			msg.Source = source
		}
		return &msg, nil
	case []byte:
		return &message{
			Source: source,
			Data:   in,
		}, nil
	}
	// never happens
	return nil, errors.New("invalid type")
}

func (m *message) Marshal() ([]byte, error) {
	return cbor.Marshal(m)
}

func (m *message) GetData() []byte {
	return m.Data
}

func (m *message) GetTopic() string {
	return m.topic
}

func (m *message) GetSource() string {
	return m.Source
}
