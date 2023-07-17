package service

import (
	"fmt"

	"github.com/fxamacker/cbor/v2"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	moodyCommon "github.com/taubyte/go-interfaces/moody"
	seerCommon "github.com/taubyte/odo/protocols/seer/common"
)

func (srv *Service) subscribe() error {
	return srv.node.PubSubSubscribe(
		seerCommon.OraclePubSubPath,
		func(msg *pubsub.Message) {
			srv.pubsubMsgHandler(msg)
		},
		func(err error) {
			// re-establish if fails
			if err.Error() != "context canceled" {
				logger.Errorf("seer pubsub subscription to `%s` had an error: %s", seerCommon.OraclePubSubPath, err.Error())
				if err := srv.subscribe(); err != nil {
					logger.Errorf("resubscribe to `%s` failed with: %s", seerCommon.OraclePubSubPath, err)
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
			logger.Error(moodyCommon.Object{"msg": fmt.Sprintf("Failed unmarshalling node data with %s", err.Error())})
			return
		}

		_, err = srv.oracle.insertHandler(node.Cid, node.Services)
		if err != nil {
			logger.Error(moodyCommon.Object{"msg": fmt.Sprintf("Failed inserting node data with %s", err.Error())})
			return
		}
	}
}
