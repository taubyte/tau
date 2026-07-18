package methods

import (
	"errors"
	"strings"

	"github.com/taubyte/tau/pkg/specs/common"
	slices "github.com/taubyte/tau/utils/slices/string"
)

func HttpPath(fqdn string, resourceType common.PathVariable) (*common.TnsPath, error) {
	if fqdn == "" {
		return nil, errors.New("fqdn is empty")
	}

	array_to_reverse := strings.Split(fqdn, ".")
	reversed := slices.ReverseArray(array_to_reverse)

	return common.NewTnsPath(append([]string{"http", string(resourceType)}, reversed...)), nil
}

// ReversedFqdnBasicPath is the domain basic-index path <resourceType>/<reversed
// fqdn segments> — the fqdn split on "." and reversed, prefixed by the resource
// type. It shares HttpPath's reversal but drops the "http" segment; the domain
// spec's BasicPath delegates here.
func ReversedFqdnBasicPath(fqdn string, resourceType common.PathVariable) (*common.TnsPath, error) {
	if fqdn == "" {
		return nil, errors.New("fqdn is empty")
	}

	array_to_reverse := strings.Split(fqdn, ".")
	reversed := slices.ReverseArray(array_to_reverse)

	return common.NewTnsPath(append([]string{string(resourceType)}, reversed...)), nil
}
