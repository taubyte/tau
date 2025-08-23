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
		logger.Errorf("Reading lock for `%s` failed with: %s", jid, err.Error())
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
		return nil, fmt.Errorf("failed cbor marshal with error: %v", err)
	}

	if err = p.db.Put(ctx, ("/locked/jobs/" + jid), lockData); err != nil {
		return nil, fmt.Errorf("locking `%s` failed with: %v", jid, err)
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
	// Grab job from active jobs
	getJob, err := p.db.Get(ctx, "/jobs/"+jid)
	if err != nil {
		return fmt.Errorf("failed getting job in updateStatus %s with error: %w", jid, err)
	}

	if err = cbor.Unmarshal(getJob, &job); err != nil {
		return fmt.Errorf("failed unmarshalling job with error: %w", err)
	}

	// Assign values
	job.Status = status
	job.Logs = cid_log
	job.AssetCid = assetCid
	job.Attempt++

	// Marshal the updated job
	jobData, err := cbor.Marshal(&job)
	if err != nil {
		return fmt.Errorf("marshal in updateStatus error: %w", err)
	}

	// Since KV store is CRDT, we can just put the job in the appropriate location
	// The CRDT will handle consistency, and clients will check all locations
	if job.Status == patrick.JobStatusSuccess || job.Status == patrick.JobStatusCancelled || job.Attempt > servicesCommon.MaxJobAttempts {
		// Job is finished - put in archive
		if err = p.db.Put(ctx, "/archive/jobs/"+jid, jobData); err != nil {
			return fmt.Errorf("failed to archive job %s: %w", jid, err)
		}
	} else {
		// Job needs retry - update in place
		if err = p.db.Put(ctx, "/jobs/"+jid, jobData); err != nil {
			return fmt.Errorf("failed to update job %s: %w", jid, err)
		}
	}

	// Clean up the lock - this is best effort since the job state is already updated
	if err := p.db.Delete(ctx, "/locked/jobs/"+jid); err != nil {
		logger.Warnf("Failed to delete lock for job %s: %v", jid, err)
	}

	return nil
}

func (p *PatrickService) republishJob(ctx context.Context, jid string) error {
	// Check if job is done otherwise delete entries and send it out again
	if _, err := p.getJob(ctx, "/archive/jobs/", jid); err == nil {
		// Job is already archived, nothing to republish
		return nil
	}

	// Job is not archived, get it from active jobs
	job, err := p.getJob(ctx, "/jobs/", jid)
	if err != nil {
		return fmt.Errorf("could not find job %s with %w", jid, err)
	}

	// Update timestamp for republishing
	job.Timestamp = time.Now().Unix()
	job_bytes, err := cbor.Marshal(job)
	if err != nil {
		return fmt.Errorf("failed to marshal job %s with error: %w", jid, err)
	}

	// Update the job in place - CRDT will handle consistency
	if err := p.db.Put(ctx, "/jobs/"+jid, job_bytes); err != nil {
		return fmt.Errorf("failed putting %s in /jobs/ inside republishJob with %w", jid, err)
	}

	// Clean up the lock - best effort
	if err := p.db.Delete(ctx, "/locked/jobs/"+jid); err != nil {
		logger.Warnf("Failed to delete lock for job %s: %v", jid, err)
	}

	// Send the job over again to run again
	if err = p.node.PubSubPublish(ctx, patrickSpecs.PubSubIdent, job_bytes); err != nil {
		return fmt.Errorf("failed to send over in republishJob pubsub error: %w", err)
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
