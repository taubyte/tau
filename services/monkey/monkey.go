package monkey

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/taubyte/tau/core/services/patrick"
	protocolCommon "github.com/taubyte/tau/services/common"
)

func (m *Monkey) Run() {
	defer func() {
		if !protocolCommon.MockedPatrick {
			m.Service.monkeysLock.Lock()
			defer m.Service.monkeysLock.Unlock()
			delete(m.Service.monkeys, m.Job.Id)
		}
	}()

	errs := make(chan error, 1024)

	m.run(errs)
	close(errs)

	runErr := m.appendErrors(m.logFile, errs)
	cid, err := m.storeLogs(m.logFile)
	if err != nil {
		logger.Errorf("Writing cid of job `%s` failed: %s", m.Id, err.Error())
	}

	m.Job.Logs[m.Job.Id] = cid
	m.LogCID = cid

	// Zombie protection: verify we still own the job before reporting results.
	// If assignment timed out and the job was given to another Monkey, discard.
	assigned, assignErr := m.Service.patrickClient.IsAssigned(m.Id)
	if assignErr != nil {
		logger.Errorf("IsAssigned check for job `%s` failed: %s", m.Id, assignErr.Error())
	}
	if !assigned {
		logger.Infof("Job `%s` is no longer assigned to us, discarding results", m.Id)
		return
	}

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
	if err := m.RunJob(); err != nil {
		appendAndLogError(errs, "Running job `%s` failed with error: %s", m.Id, err.Error())
	} else {
		m.logFile.Seek(0, io.SeekEnd)
		fmt.Fprintf(m.logFile, "\nRunning job `%s` was successful\n", m.Id)
	}
}

// newMonkey creates a Monkey for a job that was already dequeued and assigned by Patrick.
func (s *Service) newMonkey(job *patrick.Job) (*Monkey, error) {
	jid := job.Id

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
		generatedDomainRegExp: s.config.GeneratedDomainRegExp(),
		start:                 time.Now(),
	}

	m.ctx, m.ctxC = context.WithCancel(s.ctx)

	s.monkeysLock.Lock()
	s.monkeys[jid] = m
	s.monkeysLock.Unlock()

	return m, nil
}
