package service

import (
	"fmt"

	"github.com/fxamacker/cbor/v2"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	moodyCommon "github.com/taubyte/go-interfaces/moody"
	"github.com/taubyte/go-interfaces/services/patrick"
)

func (srv *Service) pubsubMsgHandler(msg *pubsub.Message) {
	var receivedJob patrick.Job
	err := cbor.Unmarshal(msg.Data, &receivedJob)
	if err != nil {
		logger.Error(moodyCommon.Object{"msg": fmt.Sprintf("Subscription unmarshal had an error: %s", err.Error())})
		return
	} else {
		if len(receivedJob.Id) == 0 {
			logger.Error(moodyCommon.Object{"msg": "Got an empty job."})
			return
		}

		//Dirty fix for now
		receivedJob.Logs = make(map[string]string)
		receivedJob.AssetCid = make(map[string]string)

		_, ok := srv.monkeys[receivedJob.Id]
		if ok {
			logger.Debug(moodyCommon.Object{"msg": fmt.Sprintf("Already processing job: `%s`", receivedJob.Id)})
			return
		}

		monkey, err := srv.newMonkey(&receivedJob)
		if err != nil {
			logger.Error(moodyCommon.Object{"msg": fmt.Sprintf("New monkey had an error: `%s`", err.Error())})
			return
		}

		go monkey.Run()
	}
}
