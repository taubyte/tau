package monkey

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"math/big"
	"os"
	"strings"
	"time"

	"github.com/ipfs/go-log/v2"
	"github.com/taubyte/tau/core/services/patrick"
	protocolCommon "github.com/taubyte/tau/services/common"
	chidori "github.com/taubyte/utils/logger/zap"
)

func (m *Monkey) Run() {

	defer func() {
		// Free the jobID from monkey
		if !protocolCommon.MockedPatrick {
			m.Service.monkeysLock.Lock()
			defer m.Service.monkeysLock.Unlock()
			delete(m.Service.monkeys, m.Job.Id)
		}
	}()

	errs := make(chan error, 1024)
	gotIt := true

	m.Status = patrick.JobStatusLocked
	isLocked, err := m.Service.patrickClient.IsLocked(m.Id)
	if !isLocked {
		appendAndLog(errs, "Locking job %s failed", m.Id)
		gotIt = false
	}
	if err != nil {
		appendAndLog(errs, "Checking if locked job %s failed with: %s", m.Id, err.Error())
		gotIt = false
	}

	if !gotIt {
		return
	}

	ctx, ctxC := context.WithCancel(m.ctx)

	go func() {
		m.run(errs)
		ctxC()
	}()

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(protocolCommon.DefaultRefreshLockTime):

				eta := time.Since(m.start) + protocolCommon.DefaultLockTime

				// TODO: handle error. Cancel context if fails multiple times
				// IDEA: have a Refresh call that actually can update logs
				m.Service.patrickClient.Lock(m.Id, uint32(eta/time.Second))
			}
		}
	}()

	<-ctx.Done()

	m.appendErrors(m.logFile, errs)
	cid, err0 := m.storeLogs(m.logFile)
	if err0 != nil {
		logger.Errorf("Writing cid of job `%s` failed: %s", m.Id, err0.Error())
	}

	m.Job.Logs[m.Job.Id] = cid //FIXME: maybe have some other kind of index for m.Job.Logs, like Timestamp
	m.LogCID = cid
	if err != nil {
		if strings.Contains(err.Error(), protocolCommon.RetryErrorString) {
			delete(m.Service.monkeys, m.Job.Id)

			if err = m.Service.patrickClient.Unlock(m.Id); err != nil {
				logger.Errorf("Unlocking job failed `%s` failed with: %s", m.Id, err.Error())
			}
		} else {
			if err = m.Service.patrickClient.Failed(m.Id, m.Job.Logs, m.Job.AssetCid); err != nil {
				logger.Errorf("Marking job failed `%s` failed with: %s", m.Id, err.Error())
			}
			m.Status = patrick.JobStatusFailed
		}
	} else {
		if err = m.Service.patrickClient.Done(m.Id, m.Job.Logs, m.Job.AssetCid); err != nil {
			logger.Errorf("Marking job done `%s` failed: %s", m.Id, err.Error())
			m.Status = patrick.JobStatusFailed
		} else {
			m.Status = patrick.JobStatusSuccess
		}
	}

	// Stash the logs
	if _, err = m.Service.hoarderClient.Stash(cid); err != nil {
		logger.Errorf("Hoarding cid `%s` of job `%s` failed: %s", cid, m.Id, err.Error())
	}

}

func (m *Monkey) run(errs chan error) {
	if err := m.RunJob(); err != nil {
		appendAndLog(errs, "Running job `%s` failed with error: %s", m.Id, err.Error())
	} else {
		m.logFile.Seek(0, io.SeekEnd)
		m.logFile.WriteString(chidori.Format(logger, log.LevelInfo, "\nRunning job `%s` was successful\n", m.Id))
	}
}

func (s *Service) newMonkey(job *patrick.Job) (*Monkey, error) {
	jid := job.Id
	err := s.patrickClient.Lock(jid, uint32(protocolCommon.DefaultLockTime/time.Second))
	if err != nil {
		return nil, err
	}

	if !protocolCommon.MockedPatrick {
		randSleep()
	}

	locked, err := s.patrickClient.IsLocked(jid)
	if err != nil {
		return nil, err
	}

	if locked {
		logFile, err := os.CreateTemp("/tmp", fmt.Sprintf("log-%s", jid))
		if err != nil {
			return nil, err
		}

		m := &Monkey{
			Id:                    jid,
			Status:                patrick.JobStatusOpen,
			Service:               s,
			Job:                   job,
			logFile:               logFile,
			generatedDomainRegExp: s.config.GeneratedDomainRegExp,
			start:                 time.Now(),
		}

		m.ctx, m.ctxC = context.WithCancel(s.ctx)

		m.Service.monkeysLock.Lock()
		s.monkeys[jid] = m
		m.Service.monkeysLock.Unlock()

		return m, nil
	}

	return nil, fmt.Errorf("didn't actually lock")
}

// Random sleep so job is unlocked randomly.
func randSleep() {
	// Using int 1<<53 as per math/rand documentation for 53 bit int.
	n, err := rand.Int(rand.Reader, big.NewInt(1<<53))
	if err != nil {
		time.Sleep(protocolCommon.DefaultLockMinWaitTime)
		return
	}

	// Convert to random value between 0 and 1 nanoseconds to value between 30 and 60 seconds
	duration := protocolCommon.DefaultLockMinWaitTime + time.Duration(float64(n.Int64())/float64(1<<53))*protocolCommon.DefaultLockMinWaitTime
	time.Sleep(duration)
}
