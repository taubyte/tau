package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/fxamacker/cbor/v2"
	commonIface "github.com/taubyte/tau/core/services/patrick"
	"github.com/taubyte/tau/p2p/streams"
	"github.com/taubyte/tau/p2p/streams/command"
	cr "github.com/taubyte/tau/p2p/streams/command/response"
	patrickSpecs "github.com/taubyte/tau/pkg/specs/patrick"
	servicesCommon "github.com/taubyte/tau/services/common"
	"github.com/taubyte/utils/maps"
)

func (p *PatrickService) requestServiceHandler(ctx context.Context, conn streams.Connection, body command.Body) (cr.Response, error) {
	cidMap := make(map[string]string, 0)
	assetMap := make(map[string]string, 0)
	action, err := maps.String(body, "action")
	if err != nil {
		return nil, fmt.Errorf("failed getting aciton from body with error: %v", err)
	}

	// Ignoring ok since we want it to either exist or just be an empty
	logCid := body["cid"]
	assetCid := body["assetCid"]

	if assetCid != nil {
		assetMap, err = convertToStringMap(assetCid)
		if err != nil {
			return nil, err
		}
	}
	if logCid != nil {
		cidMap, err = convertToStringMap(logCid)
		if err != nil {
			return nil, err
		}
	}

	jid, err := maps.String(body, "jid")
	if err != nil {
		return nil, fmt.Errorf("failed getting jid from body with error: %v", err)
	}
	switch action {
	case "list":
		return p.listHandler(ctx)
	case "info":
		return p.infoHandler(ctx, jid)
	case "lock":
		eta, err := maps.Int(body, "eta")
		if err != nil {
			return nil, fmt.Errorf("failed getting eta from body with error: %v", err)
		}

		return p.lockHandler(ctx, jid, int64(eta), conn)
	case "isLocked":
		return p.isLockedHandler(ctx, jid, conn)
	case "unlock":
		// TODO: delete
		return p.unlockHandler(ctx, jid)
	case "cancel":
		return cr.Response{"cancelled": jid}, p.cancelHandler(ctx, jid, cidMap)
	case "done":
		return nil, p.doneHandler(ctx, jid, cidMap, assetMap, conn)
	case "failed":
		return nil, p.failedHandler(ctx, jid, cidMap, assetMap, conn)
	case "timeout":
		return nil, p.timeoutHandler(ctx, jid, cidMap)
	}

	return nil, nil
}

func (p *PatrickService) listHandler(ctx context.Context) (cr.Response, error) {
	jobIds := make([]string, 0)
	jobs, err := p.db.List(ctx, "/jobs/")
	if err != nil {
		return nil, fmt.Errorf("failed getting jobs with error: %v", err)
	}

	jobIds = append(jobIds, jobs...)

	jobs, err = p.db.List(ctx, "/archive/jobs/")
	if err != nil {
		return nil, fmt.Errorf("failed getting archive jobs with error: %v", err)
	}

	jobIds = append(jobIds, jobs...)

	for index, id := range jobIds {
		list := strings.Split(id, "/jobs/")
		if len(list) > 1 {
			jobIds[index] = list[1]
		}
	}

	return cr.Response{"Ids": jobIds}, nil
}

func (p *PatrickService) infoHandler(ctx context.Context, jid string) (cr.Response, error) {
	var job commonIface.Job
	// Try getting from /archive/jobs/ if not found try  /jobs/
	jobByte, err := p.db.Get(ctx, "/jobs/"+jid)
	if err != nil {
		jobByte, err = p.db.Get(ctx, "/archive/jobs/"+jid)
		if err != nil {
			return nil, fmt.Errorf("could not find %s in /archive/jobs or /jobs", jid)
		}
	}

	err = cbor.Unmarshal(jobByte, &job)
	if err != nil {
		return nil, fmt.Errorf("unmarshal job %s failed with %w", jid, err)
	}

	return cr.Response{"job": &job}, nil
}

func (p *PatrickService) lockHandler(ctx context.Context, jid string, eta int64, conn streams.Connection) (cr.Response, error) {
	// Check if job is already registered in the lock
	var lockData []byte
	lockData, err := p.db.Get(ctx, "/locked/jobs/"+jid)
	if err != nil {
		return p.tryLock(ctx, conn.RemotePeer(), jid, time.Now().Unix(), eta)
	} else {
		resp, err := p.lockHelper(ctx, conn.RemotePeer(), lockData, jid, eta, true)
		if err != nil {
			return nil, fmt.Errorf("error in lockHandler %w", err)
		}
		return resp, nil

	}

}

func (p *PatrickService) isLockedHandler(ctx context.Context, jid string, conn streams.Connection) (cr.Response, error) {
	lockData, err := p.db.Get(ctx, "/locked/jobs/"+jid)
	if err == nil {
		resp, err := p.lockHelper(ctx, conn.RemotePeer(), lockData, jid, 0, false)
		if resp != nil {
			return resp, err
		}
	}

	return cr.Response{"locked": false}, nil
}

// TODO: Check if there is a reason why we do not return errors
func (p *PatrickService) unlockHandler(ctx context.Context, jid string) (cr.Response, error) {
	lockData, err := p.db.Get(ctx, "/locked/jobs/"+jid)
	if err != nil {
		return nil, err
	}
	var jobLock Lock
	if err = cbor.Unmarshal(lockData, &jobLock); err != nil {
		logger.Errorf("Unamrshal for `%s` failed with: %s", jid, err.Error())
	}

	jobLock.Eta = 0
	jobLock.Timestamp = 0
	lockBytes, err := cbor.Marshal(jobLock)
	if err != nil {
		logger.Errorf("Marshal for `%s` failed with: %s", jid, err.Error())
	}

	err = p.db.Put(ctx, "/locked/jobs/"+jid, lockBytes)
	if err != nil {
		logger.Errorf("Putting locked job for `%s` failed with: %s", jid, err.Error())
	}

	return cr.Response{"unlocked": jid}, nil
}

func (p *PatrickService) timeoutHandler(ctx context.Context, jid string, cid_log map[string]string) error {
	// Check if job was moved to archive jobs
	if _, err := p.getJob(ctx, "/archive/jobs/", jid); err != nil {
		job, err := p.getJob(ctx, "/jobs/", jid)
		if err != nil {
			return fmt.Errorf("failed finding job %s in timeoutHandler with %v", jid, err)
		}

		if job.Attempt == servicesCommon.MaxJobAttempts {
			job.Status = commonIface.JobStatusFailed
			job.Logs = cid_log

			jobData, err := cbor.Marshal(job)
			if err != nil {
				return fmt.Errorf("marshal in updateStatus error: %w", err)
			}

			err = p.db.Put(ctx, "/archive/jobs/"+jid, jobData)
			if err != nil {
				return fmt.Errorf("failed put in timeoutHandler with %w", err)
			}

			err = p.deleteJob(ctx, []string{"/locked/jobs/", "/jobs/"}, jid)
			if err != nil {
				return fmt.Errorf("failed delete in timeoutHandler with %w", err)
			}

			return nil
		}

		// Update attempt and timestamp and status
		job.Attempt++
		job.Timestamp = time.Now().Unix()
		job.Status = commonIface.JobStatusOpen

		// remove it from the locked list
		if err = p.db.Delete(ctx, "/locked/jobs/"+jid); err != nil {
			return fmt.Errorf("failed deleting job %s in /locked/jobs/ with error: %w", jid, err)
		}

		job_bytes, err := cbor.Marshal(job)
		if err != nil {
			return fmt.Errorf("failed to marshal job %s with error: %w", jid, err)
		}

		if err = p.db.Put(ctx, "/jobs/"+jid, job_bytes); err != nil {
			return err
		}

		// Send the job over again to run again
		if err = p.node.PubSubPublish(ctx, patrickSpecs.PubSubIdent, job_bytes); err != nil {
			return fmt.Errorf("failed to send over pubsub error: %w", err)
		}

		return nil
	}

	return fmt.Errorf("%s already finished", jid)
}

func (p *PatrickService) doneHandler(ctx context.Context, jid string, cid_log map[string]string, assetCid map[string]string, conn streams.Connection) error {
	return p.updateStatus(ctx, conn.RemotePeer(), jid, cid_log, commonIface.JobStatusSuccess, assetCid)
}

func (p *PatrickService) failedHandler(ctx context.Context, jid string, cid_log map[string]string, assetCid map[string]string, conn streams.Connection) error {
	return p.updateStatus(ctx, conn.RemotePeer(), jid, cid_log, commonIface.JobStatusFailed, assetCid)
}

func (p *PatrickService) cancelHandler(ctx context.Context, jid string, cid_log map[string]string) error {
	return p.updateStatus(ctx, "", jid, cid_log, commonIface.JobStatusCancelled, nil)
}

func (p *PatrickService) deleteJob(ctx context.Context, loc []string, jid string) error {
	for _, _loc := range loc {
		if err := p.db.Delete(ctx, _loc+jid); err != nil {
			return fmt.Errorf("failed deleting %s at %s with %v", jid, _loc, err)
		}
	}

	return nil
}
