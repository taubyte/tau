package monkey

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/taubyte/tau/core/services/monkey"
	"github.com/taubyte/tau/core/services/patrick"
	"github.com/taubyte/tau/dream"
	"github.com/taubyte/tau/p2p/peer"
)

type MonkeyTestContext struct {
	universe     *dream.Universe
	client       monkey.Client
	jid          string
	expectStatus patrick.JobStatus
	expectLog    string
}

func (c *MonkeyTestContext) waitForStatus() error {
	test := func() error {
		response, err := c.client.Status(c.jid)
		if err != nil {
			return err
		}

		// Read logs
		err = readLogsTestHelper(c.universe.Context(), response, c.universe.Monkey().Node(), c.expectLog)
		if err != nil {
			return err
		}

		// Check status
		if response.Status != c.expectStatus {
			return fmt.Errorf("job was not successful `%v != %v`", response.Status, c.expectStatus)
		}

		return nil
	}

	// ==== Wait for job ====
	var lastErr error
	for deadline := time.Now().Add(100 * time.Second); ; {
		lastErr = test()
		if lastErr == nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if lastErr != nil {
		return fmt.Errorf("test failed after waiting with: %s", lastErr.Error())
	}

	return nil
}

func readLogsTestHelper(testCtx context.Context, response *monkey.StatusResponse, peerC peer.Node, expected_logs string) error {
	cid_of_logs := response.Logs
	if len(cid_of_logs) == 0 {
		return fmt.Errorf("logs cid not found")
	}

	// Delete locally
	err := peerC.DeleteFile(cid_of_logs)
	if err != nil {
		return fmt.Errorf("Deleting logs `%s` failed with: %s", cid_of_logs, err.Error())
	}
	// After a local delete, the content has to be re-fetched from a remote
	// peer over the network — poll rather than guessing a fixed delay.
	var rs peer.ReadSeekCloser
	for deadline := time.Now().Add(6 * time.Second); ; {
		rs, err = peerC.GetFile(testCtx, cid_of_logs)
		if err == nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		return fmt.Errorf("Getting log filed failed with %s", err.Error())
	}
	// Read the logs
	logs, err := io.ReadAll(rs)
	if err != nil {
		return fmt.Errorf("Reading log file failed with %s", err.Error())
	}

	if expected_logs != string(logs) {
		return fmt.Errorf("Logs CID(`%s`)don't match, expected `%s` got `%s`", cid_of_logs, expected_logs, logs)
	}

	return nil
}
