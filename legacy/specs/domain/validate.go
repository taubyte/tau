//go:build !wasi && !wasm
// +build !wasi,!wasm

package domainSpec

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/ipfs/go-cid"
	dv "github.com/taubyte/domain-validation"
)

// TODO move to github.com/taubyte/domain-validation
func ValidateDNS(generatedRegex *regexp.Regexp, _project, host string, dev bool, options ...dv.Option) error {
	if generatedRegex != nil && generatedRegex.MatchString(host) {
		if dev {
			// For dev mode Pad project string with 0s to be at least 8 characters, otherwise the check below will panic
			if len(_project) < 8 {
				_project = fmt.Sprintf("%08s", _project)
			}
		}

		// TODO: use a regex or at least a hasprefix here. think of (somethig)-(prj:8)
		// Confirm host contains last 8 of project id
		if !strings.Contains(host, strings.ToLower(_project[len(_project)-8:])) {
			return fmt.Errorf("generated fqdn `%s` does not contain last 8 of project id %s", host, _project)
		}

		return nil
	} else if !dev {
		// Validate project CID
		project, err := cid.Decode(_project)
		if err != nil {
			return fmt.Errorf("decoding cid `%s` failed with: %s", _project, err)
		}

		// Check if domain is registered
		if err = dv.FromDNS(context.Background(), &project, host, options...); err != nil {
			return fmt.Errorf("verifying DNS failed with: %w ", err)
		}
	}

	return nil
}

func ExtractHost(hostAndPort string) string {
	return strings.ToLower(strings.Split(hostAndPort, ":")[0])
}
