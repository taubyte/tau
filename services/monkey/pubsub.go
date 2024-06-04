package monkey

import (
	"github.com/fxamacker/cbor/v2"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/taubyte/tau/core/services/patrick"
)

func (srv *Service) pubsubMsgHandler(msg *pubsub.Message) {
	var receivedJob patrick.Job
	err := cbor.Unmarshal(msg.Data, &receivedJob)
	if err != nil {
		logger.Error("Subscription unmarshal had an error:", err.Error())
		return
	} else {
		if len(receivedJob.Id) == 0 {
			logger.Error("Got an empty job.")
			return
		}

		//Dirty fix for now
		receivedJob.Logs = make(map[string]string)
		receivedJob.AssetCid = make(map[string]string)

		srv.monkeysLock.RLock()
		_, ok := srv.monkeys[receivedJob.Id]
		srv.monkeysLock.RUnlock()
		if ok {
			logger.Debugf("Already processing job: `%s`", receivedJob.Id)
			return
		}

		monkey, err := srv.newMonkey(&receivedJob)
		if err != nil {
			logger.Error("New monkey had an error:", err.Error())
			return
		}

		go monkey.Run()
	}
}
