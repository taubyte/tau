package service

import (
	"context"
	"fmt"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/taubyte/tau/core/services/patrick"
	cr "github.com/taubyte/tau/p2p/streams/command/response"
	patrickSpecs "github.com/taubyte/tau/pkg/specs/patrick"
	servicesCommon "github.com/taubyte/tau/services/common"
)

// Helper just to check if a job is already registered as lock
func (p *PatrickService) lockHelper(ctx context.Context, pid peer.ID, lockData []byte, jid string, eta int64, method bool) (cr.Response, error) {
	var jobLock Lock
	err := cbor.Unmarshal(lockData, &jobLock)
	if err != nil {
		logger.Errorf("failed to read lock for `%s`: %s", jid, err.Error())
		// TODO: probably return an error so monkey reties
		return nil, err
	} else if jobLock.Timestamp+jobLock.Eta > time.Now().Unix() {
		if method {
			if jobLock.Pid == pid {
				return p.tryLock(ctx, pid, jid, jobLock.Timestamp, eta)
			} else {
				return cr.Response{
					"locked":    true,
					"locked-by": jobLock.Pid.String(),
					"till":      jobLock.Timestamp + jobLock.Eta,
				}, fmt.Errorf("job is locked by `%s`", jobLock.Pid)
			}
		}

		return cr.Response{"locked": true, "locked-by": jobLock.Pid.String()}, nil
	}

	if method {
		return p.tryLock(ctx, pid, jid, jobLock.Timestamp, eta)
	}

	return cr.Response{"locked": false}, nil
}

// Helper just to check if a job is already registered as lock
func (p *PatrickService) tryLock(ctx context.Context, pid peer.ID, jid string, timestamp, eta int64) (cr.Response, error) {
	lockData, err := cbor.Marshal(Lock{
		Pid:       pid, // monkey ID
		Timestamp: timestamp,
		Eta:       eta,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal lock: %v", err)
	}

	if err = p.db.Put(ctx, ("/locked/jobs/" + jid), lockData); err != nil {
		return nil, fmt.Errorf("failed to lock `%s`: %v", jid, err)
	}

	return nil, nil
}

// Helper for done/failed/cancel Handler
func (p *PatrickService) updateStatus(ctx context.Context, pid peer.ID, jid string, cid_log map[string]string, status patrick.JobStatus, assetCid map[string]string) error {

	if pid != "" {
		lockData, err := p.db.Get(ctx, "/locked/jobs/"+jid)
		if err == nil {
			var jobLock Lock
			if err := cbor.Unmarshal(lockData, &jobLock); err == nil {
				if pid != peer.ID(jobLock.Pid) {
					return fmt.Errorf("failed to update job %s, %s is not the owner", jid, pid)
				}
			}
		}
	}

	var job patrick.Job
	// Grab job and move it to /archive/jobs/{jid}
	getJob, err := p.db.Get(ctx, "/jobs/"+jid)
	if err != nil {
		return fmt.Errorf("failed to get job %s: %w", jid, err)
	}

	if err = cbor.Unmarshal(getJob, &job); err != nil {
		return fmt.Errorf("failed to unmarshal job: %w", err)
	}

	// Assign values
	job.Status = status
	job.Logs = cid_log
	job.AssetCid = assetCid
	job.Attempt++

	// TODO: Un-export job locks, and create methods
	jobData, err := cbor.Marshal(&job)
	if err != nil {
		return fmt.Errorf("failed to marshal job: %w", err)
	}

	if job.Status == patrick.JobStatusSuccess || job.Status == patrick.JobStatusCancelled || job.Attempt > servicesCommon.MaxJobAttempts {
		if err = p.db.Put(ctx, "/archive/jobs/"+jid, jobData); err != nil {
			return fmt.Errorf("failed to put job in archive: %w", err)
		}
	} else {
		if err = p.db.Put(ctx, "/jobs/"+jid, jobData); err != nil {
			return fmt.Errorf("failed to put job: %w", err)
		}
	}

	if err = p.deleteJob(ctx, []string{"/locked/jobs/", "/jobs/"}, jid); err != nil {
		return fmt.Errorf("failed to delete job: %w", err)
	}

	return nil
}

func (p *PatrickService) republishJob(ctx context.Context, jid string) error {
	// Check if job is done otherwise delete entries and send it out again
	if _, err := p.getJob(ctx, "/archive/jobs/", jid); err != nil {
		job, err := p.getJob(ctx, "/jobs/", jid)
		if err != nil {
			return fmt.Errorf("failed to find job %s: %w", jid, err)
		}

		// remove it from the locked list
		if err = p.db.Delete(ctx, "/locked/jobs/"+jid); err != nil {
			return fmt.Errorf("failed to delete job %s from locked jobs: %w", jid, err)
		}

		job.Timestamp = time.Now().Unix()
		job_bytes, err := cbor.Marshal(job)
		if err != nil {
			return fmt.Errorf("failed to marshal job %s: %w", jid, err)
		}

		if err = p.db.Put(ctx, "/jobs/"+jid, job_bytes); err != nil {
			return fmt.Errorf("failed to put job %s in jobs: %w", jid, err)
		}

		// Send the job over again to run again

		if err = p.node.PubSubPublish(ctx, patrickSpecs.PubSubIdent, job_bytes); err != nil {
			return fmt.Errorf("failed to publish job: %w", err)
		}
	}

	return nil
}

func convertToStringMap(_map interface{}) (map[string]string, error) {
	newMap := make(map[string]string, 0)
	stringMap, ok := _map.(map[interface{}]interface{})
	if !ok {
		return nil, fmt.Errorf("failed converting map to map[string][string]")
	}

	for key, value := range stringMap {
		strKey := fmt.Sprintf("%v", key)
		strValue := fmt.Sprintf("%v", value)
		newMap[strKey] = strValue
	}

	return newMap, nil
}
