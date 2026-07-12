package tests

import (
	"fmt"
	"time"

	"github.com/taubyte/tau/core/services/monkey"
	"github.com/taubyte/tau/core/services/patrick"
)

func waitForTestStatus(client monkey.Client, jid string, wantStatus patrick.JobStatus) error {
	var lastErr error
	var lastStatus patrick.JobStatus
	for deadline := time.Now().Add(60 * time.Second); ; {
		response, err := client.Status(jid)
		if err == nil && response.Status == wantStatus {
			return nil
		}
		lastErr = err
		if err == nil {
			lastStatus = response.Status
		}
		if time.Now().After(deadline) {
			if lastErr != nil {
				return fmt.Errorf("test failed after waiting: %w", lastErr)
			}
			return fmt.Errorf("test failed after waiting: status is %v, want %v", lastStatus, wantStatus)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
