package monkey

import (
	"time"
)

// concurrentJobs is how many jobs a monkey runs at once, so a website or
// library build does not hold up the project's code build. Each running job
// renews its claim (Monkey.refreshClaim), so a busy monkey keeps its jobs; the
// builds themselves share the node-wide build slots.
const concurrentJobs = 3

// pollJobs periodically asks Patrick for available jobs.
func (srv *Service) pollJobs() {
	// A queued job waits at most this long for an idle monkey to notice it.
	interval := time.Second
	concurrent := concurrentJobs
	if srv.dev {
		interval = 500 * time.Millisecond
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
	job := srv.dequeueFromAny(srv.discoverPatrickPeers())
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
