package tccUtils

import (
	"fmt"

	tnsIface "github.com/taubyte/tau/core/services/tns"
	specsCommon "github.com/taubyte/tau/pkg/specs/common"
	"github.com/taubyte/tau/pkg/specs/methods"
)

// Publish publishes the compiled object and indexes to TNS
func Publish(
	tns tnsIface.Client,
	object map[string]interface{},
	indexes map[string]interface{},
	projectID string,
	branch string,
	commit string,
) error {
	if indexes == nil || object == nil {
		return fmt.Errorf("object and indexes must not be nil")
	}

	// Publish indexes
	err := tns.Push([]string{}, indexes)
	if err != nil {
		return fmt.Errorf("publish index failed with: %w", err)
	}

	// Publish project object
	prefix := methods.ProjectPrefix(projectID, branch, commit)
	err = tns.Push(prefix.Slice(), object)
	if err != nil {
		return fmt.Errorf("publish project failed with: %w", err)
	}

	// Publish current commit
	err = tns.Push(
		specsCommon.Current(projectID, branch).Slice(),
		map[string]string{
			specsCommon.CurrentCommitPathVariable.String(): commit,
		},
	)
	if err != nil {
		return fmt.Errorf("publishing current commit for project `%s` on branch `%s` failed with: %w", projectID, branch, err)
	}

	return nil
}
