package monkey

import (
	"fmt"
	"time"

	"github.com/fxamacker/cbor/v2"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/taubyte/tau/core/services/patrick"
)

// already runs in a go routine
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

		srv.recvJobsLock.Lock()
		recvJobTime, ok := srv.recvJobs[receivedJob.Id]
		srv.recvJobsLock.Unlock()
		if ok && time.Since(recvJobTime) < time.Second*60 {
			logger.Debugf("Already received job: `%s`", receivedJob.Id)
			return
		}

		srv.monkeysLock.RLock()
		_, ok = srv.monkeys[receivedJob.Id]
		srv.monkeysLock.RUnlock()
		if ok {
			logger.Debugf("Already processing job: `%s`", receivedJob.Id)
			return
		}

		srv.recvJobsLock.Lock()
		srv.recvJobs[receivedJob.Id] = time.Now()
		srv.recvJobsLock.Unlock()

		//Dirty fix for now
		if receivedJob.Logs == nil {
			receivedJob.Logs = make(map[string]string)
		}
		if receivedJob.AssetCid == nil {
			receivedJob.AssetCid = make(map[string]string)
		}

		fmt.Printf("Received job: %#v\n", receivedJob)

		monkey, err := srv.newMonkey(&receivedJob)
		if err != nil {
			logger.Error("New monkey had an error:", err.Error())
			return
		}

		monkey.Run()
	}
}
