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

	"github.com/taubyte/go-interfaces/services/patrick"
	hoarderClient "github.com/taubyte/odo/clients/p2p/hoarder"
	protocolCommon "github.com/taubyte/odo/protocols/common"
)

func (m *Monkey) Run() {
	// declare started
	m.Status = patrick.JobStatusLocked
	islocked, err := m.Service.patrickClient.IsLocked(m.Id)
	if !islocked {
		errormsg := fmt.Sprintf("Locking job %s failed", m.Id)
		logger.Error(errormsg)
		m.logFile.Write([]byte(errormsg))
	}

	if err != nil {
		errormsg := fmt.Sprintf("Checking if locked job %s failed with: %s", m.Id, err.Error())
		logger.Error(errormsg)
		m.logFile.Write([]byte(errormsg))
	}

	// complete/run the actual job
	// Running job goes here

	err = m.RunJob()
	if err != nil {
		errormsg := fmt.Sprintf("Running job `%s` failed with error: %s", m.Id, err.Error())
		logger.Error(errormsg)
		m.logFile.Write([]byte(errormsg))
	} else {
		m.logFile.Write([]byte(fmt.Sprintf("Running job `%s` was successful", m.Id)))
	}

	// write the logs and save cid
	m.logFile.Seek(0, io.SeekStart)
	cid_of_logs, err0 := m.Service.node.AddFile(m.logFile)
	if err0 != nil {
		errormsg := fmt.Sprintf("Writing cid of job `%s` failed: %s", m.Id, err.Error())
		logger.Error(errormsg)
		m.logFile.Write([]byte(errormsg))
	}

	m.Job.Logs[m.Job.Id] = cid_of_logs
	if err != nil {
		if strings.Contains(err.Error(), protocolCommon.RetryErrorString) {
			// Delete from running
			delete(m.Service.monkeys, m.Job.Id)

			// unlock
			err = m.Service.patrickClient.Unlock(m.Id)
			if err != nil {
				errormsg := fmt.Sprintf("Unlocking job failed `%s` failed with: %s", m.Id, err.Error())
				logger.Error(errormsg)
				m.logFile.Write([]byte(errormsg))
			}
		} else {
			err = m.Service.patrickClient.Failed(m.Id, m.Job.Logs, m.Job.AssetCid)
			if err != nil {
				errormsg := fmt.Sprintf("Marking job failed `%s` failed with: %s", m.Id, err.Error())
				logger.Error(errormsg)
				m.logFile.Write([]byte(errormsg))
			}
			m.Status = patrick.JobStatusFailed
		}
	} else {
		err = m.Service.patrickClient.Done(m.Id, m.Job.Logs, m.Job.AssetCid)
		if err != nil {
			errormsg := fmt.Sprintf("Marking job done `%s` failed: %s", m.Id, err.Error())
			logger.Error(errormsg)
			m.logFile.Write([]byte(errormsg))
			m.Status = patrick.JobStatusFailed
		} else {
			m.Status = patrick.JobStatusSuccess
		}
	}

	hoarder, err := hoarderClient.New(m.Service.ctx, m.Service.node)
	if err != nil {
		errormsg := err.Error()
		logger.Error(errormsg)
		m.logFile.Write([]byte(errormsg))
	}

	// Stash the logs
	_, err = hoarder.Stash(cid_of_logs)
	if err != nil {
		errormsg := fmt.Sprintf("Hoarding cid `%s` of job `%s` failed: %s", cid_of_logs, m.Id, err.Error())
		logger.Error(errormsg)
		m.logFile.Write([]byte(errormsg))
	}
	m.LogCID = cid_of_logs

	// Free the jobID from monkey
	if !protocolCommon.LocalPatrick {
		delete(m.Service.monkeys, m.Job.Id)
	}
}

func (s *Service) newMonkey(job *patrick.Job) (*Monkey, error) {
	jid := job.Id
	err := s.patrickClient.Lock(jid, uint32(protocolCommon.DefaultLockTime)) //5 minutes to complete a job
	if err != nil {
		return nil, err
	}

	randSleep()
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
			Id:      jid,
			Status:  patrick.JobStatusOpen,
			Service: s,
			Job:     job,
			logFile: logFile,
		}

		m.ctx, m.ctxC = context.WithCancel(s.ctx)
		s.monkeys[jid] = m
		return m, nil
	}
	return nil, fmt.Errorf("didn't actually lock")
}

// Random sleep so job is unlocked randomly.
func randSleep() error {
	// Using int 1<<53 as per math/rand documentation for 53 bit int.
	n, err := rand.Int(rand.Reader, big.NewInt(1<<53))
	if err != nil {
		return fmt.Errorf("generating random int failed with: %s", err)
	}

	// Convert to random value between 0 and 1 nanoseconds to value between 0 and 10 seconds
	time.Sleep(time.Duration(float64(n.Int64()) / float64(1<<53) * 10000000000))
	return nil
}
