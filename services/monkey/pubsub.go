package monkey

import (
	"time"
)

// devConcurrentJobs is how many jobs a development node runs at once, so a
// website or library build does not hold up the project's code build. Off one,
// a monkey runs one job at a time: Patrick hands a job to another monkey when
// its assignment is not refreshed, and a busy monkey would not refresh it.
const devConcurrentJobs = 3

// pollJobs periodically asks Patrick for available jobs.
func (srv *Service) pollJobs() {
	interval := 5 * time.Second
	concurrent := 1
	if srv.dev {
		interval = 500 * time.Millisecond
		concurrent = devConcurrentJobs
	}
	slots := make(chan struct{}, concurrent)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-srv.ctx.Done():
			return
		case <-ticker.C:
		}

		// Take jobs while there are free slots, rather than one per tick.
	take:
		for {
			select {
			case slots <- struct{}{}:
			default:
				break take
			}
			monkey := srv.tryDequeueJob()
			if monkey == nil {
				<-slots
				break
			}
			if concurrent == 1 {
				monkey.Run()
				<-slots
				break
			}
			go func() {
				defer func() { <-slots }()
				monkey.Run()
			}()
		}
	}
}

// tryDequeueJob attempts to dequeue a single job from Patrick and returns the
// monkey that will run it, or nil when there is nothing new to run.
func (srv *Service) tryDequeueJob() *Monkey {
	peers := srv.discoverPatrickPeers()
	client := srv.patrickClient.Peers(peers...)

	job, err := client.Dequeue()
	if err != nil {
		logger.Debugf("Dequeue failed: %v", err)
		return nil
	}
	if job == nil {
		return nil
	}

	srv.recvJobsLock.Lock()
	srv.recvJobs[job.Id] = time.Now()
	srv.recvJobsLock.Unlock()

	srv.monkeysLock.RLock()
	_, ok := srv.monkeys[job.Id]
	srv.monkeysLock.RUnlock()
	if ok {
		logger.Debugf("Already processing job: %s", job.Id)
		return nil
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
		return nil
	}

	return monkey
}
