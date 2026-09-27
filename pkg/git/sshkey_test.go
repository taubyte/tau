package git

import (
	"testing"

	"gotest.tools/v3/assert"
)

// The account is a host fact, and an empty one would authenticate as nobody.
func TestSSHKeyAsNeedsAnAccount(t *testing.T) {
	assert.ErrorContains(t, SSHKeyAs("", "key")(&Repository{}), "account")
}
