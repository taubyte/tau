package tests

import (
	"fmt"
	"time"

	"github.com/taubyte/go-interfaces/services/monkey"
	"github.com/taubyte/go-interfaces/services/patrick"
)

func waitForTestStatus(client monkey.Client, jid string, wantStatus patrick.JobStatus) error {
	test := func() error {
		response, err := client.Status(jid)
		if err != nil {
			return err

		}
		if response.Status != wantStatus {
			return fmt.Errorf("job was not successful `%v != %v`", response.Status, wantStatus)
		}

		return nil
	}

	attempts := 0
	maxAttempts := 50
	cont := func() {
		attempts += 1
		time.Sleep(time.Second)
	}

	// ==== Wait for job ====
	for {
		err := test()
		if err != nil && attempts >= maxAttempts {
			return fmt.Errorf("test failed after %d attempts with: %s", attempts, err.Error())
		} else if err == nil {
			break
		}
		cont()
	}

	return nil
}
