package compile_test

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/taubyte/tau/dream"
	commonTest "github.com/taubyte/tau/dream/helpers"
	"github.com/taubyte/tau/pkg/specs/common"
	functionSpec "github.com/taubyte/tau/pkg/specs/function"
	"github.com/taubyte/tau/pkg/specs/methods"
	websiteSpec "github.com/taubyte/tau/pkg/specs/website"
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

func callHalWithRetry(u *dream.Universe, path string, maxRetries int, retryDelay time.Duration) ([]byte, error) {
	var lastErr error

	for i := 0; i < maxRetries; i++ {
		body, err := callHal(u, path)
		if err == nil {
			return body, nil
		}

		lastErr = err
		if !isLookupError(err) {
			return nil, err
		}

		if i < maxRetries-1 {
			time.Sleep(retryDelay)
		}
	}

	return nil, fmt.Errorf("failed after %d retries, last error: %w", maxRetries, lastErr)
}

func isLookupError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "http serviceable lookup failed") ||
		strings.Contains(errStr, "no HTTP match found") ||
		strings.Contains(errStr, "looking up serviceable failed")
}

func waitForWebsiteInTNS(u *dream.Universe, fqdn string, maxRetries int, retryDelay time.Duration) error {
	return waitForHTTPResourceInTNS(u, fqdn, websiteSpec.PathVariable, maxRetries, retryDelay)
}

func waitForFunctionInTNS(u *dream.Universe, fqdn string, maxRetries int, retryDelay time.Duration) error {
	return waitForHTTPResourceInTNS(u, fqdn, functionSpec.PathVariable, maxRetries, retryDelay)
}

func waitForHTTPResourceInTNS(u *dream.Universe, fqdn string, resourceType common.PathVariable, maxRetries int, retryDelay time.Duration) error {
	substrate := u.Substrate()
	if substrate == nil {
		return fmt.Errorf("substrate service not available")
	}

	tns := substrate.Tns()
	if tns == nil {
		return fmt.Errorf("TNS client not available from substrate service")
	}

	httpPath, err := methods.HttpPath(fqdn, resourceType)
	if err != nil {
		return fmt.Errorf("creating HTTP path failed: %w", err)
	}

	linksPath := httpPath.Versioning().Links()

	for i := 0; i < maxRetries; i++ {
		indexObject, err := tns.Fetch(linksPath)
		if err == nil {
			pathList, err := indexObject.Current(common.DefaultBranches)
			if err == nil && len(pathList) > 0 {
				return nil
			}
		}

		if i < maxRetries-1 {
			time.Sleep(retryDelay)
		}
	}

	return fmt.Errorf("HTTP resource not available in TNS after %d retries", maxRetries)
}
