package monkey

import (
	"time"
)

// pollJobs periodically asks Patrick for available jobs.
func (srv *Service) pollJobs() {
	interval := 5 * time.Second
	if srv.dev {
		interval = 2 * time.Second
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-srv.ctx.Done():
			return
		case <-ticker.C:
			srv.tryDequeueJob()
		}
	}
}

// tryDequeueJob attempts to dequeue a single job from Patrick and run it.
func (srv *Service) tryDequeueJob() {
	peers := srv.discoverPatrickPeers()
	client := srv.patrickClient.Peers(peers...)

	job, err := client.Dequeue()
	if err != nil {
		logger.Debugf("Dequeue failed: %v", err)
		return
	}
	if job == nil {
		return
	}

	srv.recvJobsLock.Lock()
	srv.recvJobs[job.Id] = time.Now()
	srv.recvJobsLock.Unlock()

	srv.monkeysLock.RLock()
	_, ok := srv.monkeys[job.Id]
	srv.monkeysLock.RUnlock()
	if ok {
		logger.Debugf("Already processing job: %s", job.Id)
		return
	}

	if job.Logs == nil {
		job.Logs = make(map[string]string)
	}
	if job.AssetCid == nil {
		job.AssetCid = make(map[string]string)
	}

	logger.Infof("Received job: %s", job.Id)

	monkey, err := srv.newMonkey(job)
	if err != nil {
		logger.Error("New monkey had an error:", err.Error())
		return
	}

	monkey.Run()
}
