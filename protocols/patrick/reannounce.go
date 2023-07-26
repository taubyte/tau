package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/taubyte/go-interfaces/services/patrick"
	patrickSpecs "github.com/taubyte/go-specs/patrick"
	protocolsCommon "github.com/taubyte/odo/protocols/common"
)

func (p *PatrickService) ReannounceJobs(ctx context.Context) error {
	//Grab all job id's that are still in the list
	jobs, err := p.db.List(ctx, "/jobs/")
	if err != nil {
		return fmt.Errorf("failed grabbing all jobs with error: %v", err)
	}
	if len(jobs) > 0 {
		for _, id := range jobs {
			// Doing this to only get the jid since id is /jobs/jid
			// Check if its currently locked and if it is we dont reannounce it
			jid := id[strings.LastIndex(id, "/")+1:]
			_, err := p.db.Get(ctx, "/locked/"+jid)
			if err != nil {
				err = p.republishJob(ctx, jid)
				if err != nil {
					logger.Errorf("Failed republishing job %s with error: %s", jid, err.Error())
					continue
				}
			}
		}
	}
	return nil
}

func (p *PatrickService) ReannounceFailedJobs(ctx context.Context) error {
	//Grab all job id's that are still in the list
	jobs, err := p.db.List(ctx, "/archive/jobs/")
	if err != nil {
		return fmt.Errorf("failed grabbing all jobs with error: %v", err)
	}
	if len(jobs) > 0 {
		for _, id := range jobs {
			// Get that specific job data by id
			job, err := p.getJob(ctx, id, "")
			if err != nil {
				// Continuing incase job gets schedule while routine is going
				logger.Errorf("Failed getting %s with: %s", id, err.Error())
				continue
			}

			// If already tried twice or did not fail skip it
			if job.Attempt == protocolsCommon.MaxJobAttempts || job.Status != patrick.JobStatusFailed {
				continue
			}

			// Update attemps and timestamp and status
			job.Attempt++
			job.Timestamp = time.Now().Unix()
			job.Status = patrick.JobStatusOpen

			job_byte, err := cbor.Marshal(job)
			if err != nil {
				logger.Errorf("Failed cbor marshall on job %s with: %s", id, err.Error())
				continue
			}

			// Put the job back into the list
			err = p.db.Put(ctx, "/jobs/"+job.Id, job_byte)
			if err != nil {
				logger.Errorf("Failed putting job %s into database with: %s", id, err.Error())
				continue
			}

			err = p.db.Delete(ctx, "/archive/jobs/"+job.Id)
			if err != nil {
				return fmt.Errorf("failed deleting job %s in archive/jobs with error: %w", job.Id, err)
			}

			// Send the job over pub sub
			err = p.node.PubSubPublish(ctx, patrickSpecs.PubSubIdent, job_byte)
			if err != nil {
				return fmt.Errorf("failed to send over pubsub error: %v", err)
			}
		}
	}
	return nil
}
