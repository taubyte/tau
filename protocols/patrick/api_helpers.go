package service

import (
	"context"
	"fmt"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/taubyte/go-interfaces/services/patrick"
	patrickSpecs "github.com/taubyte/go-specs/patrick"
	cr "github.com/taubyte/p2p/streams/command/response"
)

// Helper just to check if a job is already registered as lock
func (p *PatrickService) lockHelper(lockData []byte, jid string, method bool) (cr.Response, error) {
	var jobLock Lock
	if err := cbor.Unmarshal(lockData, &jobLock); err != nil {
		logger.Errorf("Reading lock for `%s` failed with: %w", jid, err)
		// continue assuming another patrick crashed when trying to lock
	} else if jobLock.Timestamp+jobLock.Eta > time.Now().Unix() {
		if method {
			return cr.Response{
				"locked-by": jobLock.Pid,
				"till":      jobLock.Timestamp + jobLock.Eta,
			}, fmt.Errorf("job is locked by `%s`", jobLock.Pid)
		}

		return cr.Response{"locked": true, "locked-by": jobLock.Pid}, nil
	}
	return nil, nil
}

// Helper for done/failed/cancel Handler
func (p *PatrickService) updateStatus(ctx context.Context, jid string, cid_log map[string]string, status patrick.JobStatus, assetCid map[string]string) error {
	var job patrick.Job
	// Grab job and move it to /archive/jobs/{jid}
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

	jobData, err := cbor.Marshal(job)
	if err != nil {
		return fmt.Errorf("marshal in updateStatus error: %w", err)
	}

	if err = p.db.Put(ctx, "/archive/jobs/"+jid, jobData); err != nil {
		return fmt.Errorf("updateStatus put failed with error: %w", err)
	}

	if err = p.deleteJob(ctx, []string{"/locked/jobs/", "/jobs/"}, jid); err != nil {
		return fmt.Errorf("failed delete in timeoutHandler with %w", err)
	}

	return nil
}

func (p *PatrickService) republishJob(ctx context.Context, jid string) error {
	// Check if job is done otherwise delete entries and send it out again
	if _, err := p.getJob(ctx, "/archive/jobs/", jid); err != nil {
		job, err := p.getJob(ctx, "/jobs/", jid)
		if err != nil {
			return fmt.Errorf("could not find job %s with %w", jid, err)
		}

		// remove it from the locked list
		if err = p.db.Delete(ctx, "/locked/jobs/"+jid); err != nil {
			return fmt.Errorf("failed deleting job %s at /locked/jobs/ with error in republishJob: %w", jid, err)
		}

		job.Timestamp = time.Now().Unix()

		job_bytes, err := cbor.Marshal(job)
		if err != nil {
			return fmt.Errorf("failed to marshal job %s with error: %w", jid, err)
		}

		if err = p.db.Put(ctx, "/jobs/"+jid, job_bytes); err != nil {
			return fmt.Errorf("failed putting %s in /jobs/ inside republishJob with %w", jid, err)
		}

		// Send the job over again to run again

		if err = p.node.PubSubPublish(ctx, patrickSpecs.PubSubIdent, job_bytes); err != nil {
			return fmt.Errorf("failed to send over in republishJob pubsub error: %w", err)
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
