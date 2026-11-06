package auth

import (
	"bufio"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	logging "github.com/ipfs/go-log/v2"
	"gotest.tools/v3/assert"
)

// ACME data holds the account key and every certificate's private key; reading
// or writing it must never put the bytes in a log, at any level.
func TestAcmeDataNeverLogged(t *testing.T) {
	const secret = "-----BEGIN EC PRIVATE KEY-----SECRETMATERIAL"

	assert.NilError(t, logging.SetLogLevel("tau.auth.service", "debug"))
	t.Cleanup(func() { logging.SetLogLevel("tau.auth.service", "error") })

	pipe := logging.NewPipeReader()
	defer pipe.Close()
	lines := make(chan string, 256)
	go func() {
		scanner := bufio.NewScanner(pipe)
		scanner.Buffer(make([]byte, 64*1024), 1<<20)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
		close(lines)
	}()

	ctx := context.Background()
	svc, err := New(ctx, newTestConfig(t, 12391))
	assert.NilError(t, err)
	defer svc.Close()

	assert.NilError(t, svc.setACMECertificate(ctx, "leak.example.com", []byte(secret)))
	assert.NilError(t, svc.setACMEStaticCertificate(ctx, "static-leak.example.com", []byte(secret)))
	assert.NilError(t, svc.setACMECache(ctx, "acme_account+key", []byte(secret)))
	_, err = svc.getACMECertificate(ctx, "leak.example.com")
	assert.NilError(t, err)
	_, err = svc.getACMEStaticCertificate(ctx, "static-leak.example.com")
	assert.NilError(t, err)
	_, err = svc.getACMECache(ctx, "acme_account+key")
	assert.NilError(t, err)

	// The last call logs its "done" line; everything before it has been written.
	// %v prints a byte slice as numbers, which is the same secret.
	asNumbers := strings.Trim(fmt.Sprint([]byte("SECRETMATERIAL")), "[]")

	deadline := time.After(2 * time.Second)
	for {
		select {
		case line, open := <-lines:
			if !open {
				t.Fatal("log pipe closed early")
			}
			if strings.Contains(line, "SECRETMATERIAL") || strings.Contains(line, asNumbers) {
				t.Fatalf("ACME data leaked into the log: %s", line)
			}
			if strings.Contains(line, "Get acme cache for `acme_account+key` done") {
				return
			}
		case <-deadline:
			t.Fatal("did not see the final ACME log line")
		}
	}
}
