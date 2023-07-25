package seer

import (
	"github.com/fxamacker/cbor/v2"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	protocolsCommon "github.com/taubyte/odo/protocols/common"
)

func (srv *Service) subscribe() error {
	return srv.node.PubSubSubscribe(
		protocolsCommon.OraclePubSubPath,
		func(msg *pubsub.Message) {
			srv.pubsubMsgHandler(msg)
		},
		func(err error) {
			// re-establish if fails
			if err.Error() != "context canceled" {
				logger.Errorf("seer pubsub subscription to `%s` had an error: %s", protocolsCommon.OraclePubSubPath, err.Error())
				if err := srv.subscribe(); err != nil {
					logger.Errorf("resubscribe to `%s` failed with: %s", protocolsCommon.OraclePubSubPath, err)
				}
			}
		},
	)
}

// TODO: Pubsub usage data to have timestamp as well
func (srv *Service) pubsubMsgHandler(msg *pubsub.Message) {
	// Only process msg not from ourselves
	if msg.ReceivedFrom != srv.node.ID() {
		var node nodeData
		err := cbor.Unmarshal(msg.Data, &node)
		if err != nil {
			logger.Errorf("Failed unmarshalling node data with %w", err)
			return
		}

		_, err = srv.oracle.insertHandler(node.Cid, node.Services)
		if err != nil {
			logger.Errorf("Failed inserting node data with %w", err)
			return
		}
	}
}
