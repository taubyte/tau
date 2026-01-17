package compile_test

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/taubyte/tau/dream"
	commonTest "github.com/taubyte/tau/dream/helpers"
)

var (
	testProjectId   = "QmegMKBQmDTU9FUGKdhPFn1ZEtwcNaCA2wmyLW8vJn7wZN"
	testFunctionId  = "QmZY4u91d1YALDN2LTbpVtgwW8iT5cK9PE1bHZqX9J51Tv"
	testFunction2Id = "QmZY4u91d1YALDN2LTbpVtgwW8iT5cK9PE1bHZqX9J5456"
	testSmartOpId   = "QmZY4u91d1YALDN2LTbpVtgwW8iT5cK9PE1bHZqX9J5123"
	testLibraryId   = "QmP6qBNyoLeMLiwk8uYZ8xoT4CnDspYntcY4oCkpVG1byt"
	testWebsiteId   = "QmcrzjxwbqERscawQcXW4e5jyNBNoxLsUYatn63E8XPQq2"
)

func callHal(u *dream.Universe, path string) ([]byte, error) {
	nodePort, err := u.GetPortHttp(u.Substrate().Node())
	if err != nil {
		return nil, err
	}

	host := fmt.Sprintf("hal.computers.com:%d", nodePort)

	ret, err := commonTest.CreateHttpClient().Get(fmt.Sprintf("http://%s%s", host, path))
	if err != nil {
		return nil, err
	}

	defer ret.Body.Close()

	return io.ReadAll(ret.Body)
}

// callHalWithRetry attempts to call the website with retries to handle timing issues
func callHalWithRetry(u *dream.Universe, path string, maxRetries int, retryDelay time.Duration) ([]byte, error) {
	var lastErr error

	for i := 0; i < maxRetries; i++ {
		body, err := callHal(u, path)
		if err == nil {
			return body, nil
		}

		lastErr = err

		// Only retry if it's a lookup error, not a connection error
		if !isLookupError(err) {
			return nil, err
		}

		if i < maxRetries-1 {
			time.Sleep(retryDelay)
		}
	}

	return nil, fmt.Errorf("failed after %d retries, last error: %w", maxRetries, lastErr)
}

// isLookupError checks if the error is related to HTTP serviceable lookup
func isLookupError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "http serviceable lookup failed") ||
		strings.Contains(errStr, "no HTTP match found") ||
		strings.Contains(errStr, "looking up serviceable failed")
}
