package monkey

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"math/big"
	"os"
	"runtime/debug"
	"time"

	"github.com/taubyte/tau/core/services/patrick"
	protocolCommon "github.com/taubyte/tau/services/common"
)

func (m *Monkey) Run() {
	fmt.Printf("Monkey %#v\n", m)
	debug.PrintStack()

	defer func() {
		if !protocolCommon.MockedPatrick {
			m.Service.monkeysLock.Lock()
			defer m.Service.monkeysLock.Unlock()
			delete(m.Service.monkeys, m.Job.Id)
		}
	}()

	errs := make(chan error, 1024)

	ctx, ctxC := context.WithCancel(m.ctx)

	doneRefresh := make(chan struct{})
	go func() {
		for {
			select {
			case <-ctx.Done():
				doneRefresh <- struct{}{}
				return
			case <-time.After(protocolCommon.DefaultRefreshLockTime):

				eta := time.Since(m.start) + protocolCommon.DefaultLockTime

				m.Service.patrickClient.Lock(m.Id, uint32(eta/time.Second))
			}
		}
	}()

	m.run(errs)
	close(errs)
	ctxC()
	fmt.Println("Context cancelled")
	<-doneRefresh
	fmt.Println("Done refreshing")

	runErr := m.appendErrors(m.logFile, errs)
	cid, err := m.storeLogs(m.logFile)
	if err != nil {
		logger.Errorf("Writing cid of job `%s` failed: %s", m.Id, err.Error())
	}

	m.Job.Logs[m.Job.Id] = cid
	m.LogCID = cid

	fmt.Printf("RunErr %#v\n", runErr)
	fmt.Printf("JOB = %#v\n", m.Job)

	if runErr != nil {
		if err = m.Service.patrickClient.Failed(m.Id, m.Job.Logs, m.Job.AssetCid); err != nil {
			logger.Errorf("Marking job failed `%s` failed with: %s", m.Id, err.Error())
		}
		m.Status = patrick.JobStatusFailed
	} else {
		if err = m.Service.patrickClient.Done(m.Id, m.Job.Logs, m.Job.AssetCid); err != nil {
			logger.Errorf("Marking job done `%s` failed: %s", m.Id, err.Error())
			m.Status = patrick.JobStatusFailed
		} else {
			m.Status = patrick.JobStatusSuccess
		}
	}

	if _, err = m.Service.hoarderClient.Stash(cid); err != nil {
		logger.Errorf("Hoarding cid `%s` of job `%s` failed: %s", cid, m.Id, err.Error())
	}

}

func (m *Monkey) run(errs chan error) {
	fmt.Printf("Running job %#v\n", m.Job)
	defer fmt.Println("Finished running job")
	if err := m.RunJob(); err != nil {
		appendAndLogError(errs, "Running job `%s` failed with error: %s", m.Id, err.Error())
	} else {
		m.logFile.Seek(0, io.SeekEnd)
		fmt.Fprintf(m.logFile, "\nRunning job `%s` was successful\n", m.Id)
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
			Status:                patrick.JobStatusLocked,
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

func randSleep() {
	n, err := rand.Int(rand.Reader, big.NewInt(1<<53))
	if err != nil {
		time.Sleep(protocolCommon.DefaultLockMinWaitTime)
		return
	}

	duration := protocolCommon.DefaultLockMinWaitTime + time.Duration(float64(n.Int64())/float64(1<<53))*protocolCommon.DefaultLockMinWaitTime
	time.Sleep(duration)
}
