package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/fxamacker/cbor/v2"
)

func (p *PatrickService) ReannounceJobs(ctx context.Context) error {
	//Grab all job id's that are still in the list
	jobs, err := p.db.List(ctx, "/jobs/")
	if err != nil {
		return fmt.Errorf("failed grabbing all jobs with error: %v", err)
	}

	for _, id := range jobs {
		// Doing this to only get the jid since id is /jobs/jid
		// Check if its currently locked and if it is we dont reannounce it
		jid := id[strings.LastIndex(id, "/")+1:]
		lockData, err := p.db.Get(ctx, "/locked/jobs/"+jid)
		if err == nil {
			var jobLock Lock
			if err := cbor.Unmarshal(lockData, &jobLock); err == nil {
				if jobLock.Timestamp+jobLock.Eta < time.Now().Unix() {
					if err = p.republishJob(ctx, jid); err != nil {
						logger.Errorf("Failed republishing job %s with error: %s", jid, err.Error())
					}
				}
			}
		}
	}

	return nil
}
