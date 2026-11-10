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

	stopLogs := m.streamLogs()
	stopRefresh := m.refreshClaim()
	m.run(errs)
	stopRefresh()
	close(errs)

	runErr := m.appendErrors(m.logFile, errs)
	stopLogs()
	cid, err := m.storeLogs(m.logFile)
	if err != nil {
		logger.Errorf("Writing cid of job `%s` failed: %s", m.Id, err.Error())
	}
	// The log is stored by CID now; the temp file would otherwise stay in /tmp.
	m.logFile.Close()
	os.Remove(m.logFile.Name())

	m.Job.Logs[m.Job.Id] = cid
	m.LogCID = cid

	// Zombie protection: verify we still own the job before reporting results.
	// If assignment timed out and the job was given to another Monkey, discard.
	// On an error, report anyway: Patrick rejects a report from a non-owner.
	assigned, assignErr := m.Service.patrickClient.IsAssigned(m.Id)
	if assignErr != nil {
		logger.Errorf("IsAssigned check for job `%s` failed: %s", m.Id, assignErr.Error())
	}
	if assignErr == nil && !assigned {
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

	if f, ferr := m.Service.node.GetFile(m.Service.node.Context(), cid); ferr != nil {
		logger.Errorf("Re-opening cid `%s` of job `%s` failed: %s", cid, m.Id, ferr.Error())
	} else {
		err = m.Service.hoarderClient.Stash(cid, f)
		f.Close()
		if err != nil {
			logger.Errorf("Hoarding cid `%s` of job `%s` failed: %s", cid, m.Id, err.Error())
		}
	}

}

// refreshClaim renews the job's claim on Patrick every DefaultRefreshLockTime
// while the build runs, and cancels the build once another Monkey owns the job.
// Errors are ignored: an old Patrick has no refresh and answers with an error.
func (m *Monkey) refreshClaim() (stop func()) {
	ctx, cancel := context.WithCancel(m.ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(protocolCommon.DefaultRefreshLockTime)
		defer ticker.Stop()
		// Two "not assigned" answers in a row before giving up: Patrick also
		// answers that way when one of its reads fails, and a single blip must
		// not throw a build away. Two intervals still fit in the claim lifetime.
		misses := 0
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				assigned, err := m.Service.patrickClient.Refresh(m.Id)
				if err != nil || assigned {
					misses = 0
					continue
				}
				if misses++; misses >= 2 {
					logger.Infof("Job `%s` is no longer assigned to us, stopping its build", m.Id)
					m.ctxC()
					return
				}
			}
		}
	}()
	return func() {
		cancel()
		<-done
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
