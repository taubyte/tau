package tests

import (
	"fmt"
	"time"

	"github.com/taubyte/tau/core/services/monkey"
	"github.com/taubyte/tau/core/services/patrick"
)

func waitForTestStatus(client monkey.Client, jid string, wantStatus patrick.JobStatus) error {
	const maxAttempts = 20
	const retryDelay = 3 * time.Second

	attempt := 0
	for {
		attempt++
		if attempt >= maxAttempts {
			return fmt.Errorf("test failed after %d attempts", maxAttempts)
		}

		response, err := client.Status(jid)
		if err == nil && response.Status == wantStatus {
			return nil
		}

		time.Sleep(retryDelay)
	}
}
