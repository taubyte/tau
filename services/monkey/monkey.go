package monkey

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/taubyte/tau/core/services/patrick"
	protocolCommon "github.com/taubyte/tau/services/common"
)

func (m *worker) Run() {
	defer func() {
		m.Service.monkeysLock.Lock()
		defer m.Service.monkeysLock.Unlock()
		delete(m.Service.monkeys, m.Job.Id)
	}()

	errs := make(chan error, 1024)
	gotIt := true

	m.Status = patrick.JobStatusLocked
	isLocked, err := m.Service.patrickClient.IsLocked(m.Id)
	if !isLocked {
		appendAndLogError(errs, "Locking job %s failed", m.Id)
		gotIt = false
	}
	if err != nil {
		appendAndLogError(errs, "Checking if locked job %s failed with: %s", m.Id, err.Error())
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

	m.Job.Logs[m.Job.Id] = cid
	m.LogCID = cid
	if err != nil {
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

func (m *worker) run(errs chan error) {
	if err := m.RunJob(); err != nil {
		appendAndLogError(errs, "Running job `%s` failed with error: %s", m.Id, err.Error())
	} else {
		m.logFile.Seek(0, io.SeekEnd)
		fmt.Fprintf(m.logFile, "\nRunning job `%s` was successful\n", m.Id)
	}
}
