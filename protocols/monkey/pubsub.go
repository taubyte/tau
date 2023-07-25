package monkey

import (
	"github.com/fxamacker/cbor/v2"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/taubyte/go-interfaces/services/patrick"
)

func (srv *Service) pubsubMsgHandler(msg *pubsub.Message) {
	var receivedJob patrick.Job
	err := cbor.Unmarshal(msg.Data, &receivedJob)
	if err != nil {
		logger.Errorf("Subscription unmarshal had an error: %w", err)
		return
	} else {
		if len(receivedJob.Id) == 0 {
			logger.Error("Got an empty job.")
			return
		}

		//Dirty fix for now
		receivedJob.Logs = make(map[string]string)
		receivedJob.AssetCid = make(map[string]string)

		_, ok := srv.monkeys[receivedJob.Id]
		if ok {
			logger.Debugf("Already processing job: `%s`", receivedJob.Id)
			return
		}

		monkey, err := srv.newMonkey(&receivedJob)
		if err != nil {
			logger.Errorf("New monkey had an error: `%w`", err)
			return
		}

		go monkey.Run()
	}
}
