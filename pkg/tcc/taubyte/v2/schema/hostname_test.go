package schema_test

import (
	"fmt"
	"strings"
	"testing"

	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	"gotest.tools/v3/assert"
)

// The same project has the same name on every cloud that hosts it, which is the point
// of deriving the label instead of assigning it.
func TestProjectLabelIsDeterministic(t *testing.T) {
	const id = "QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR"
	assert.Equal(t, schema.ProjectLabel(id), schema.ProjectLabel(id))
	assert.Assert(t, schema.ProjectLabel(id) != schema.ProjectLabel(id+"x"))
}

// A DNS label is case-insensitive, so the alphabet has to be too, and it may hold only
// letters and digits here. Anything outside that is a name that does not resolve.
func TestProjectLabelIsADnsLabel(t *testing.T) {
	label := schema.ProjectLabel("QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR")
	assert.Equal(t, len(label), schema.ProjectLabelLength)
	assert.Equal(t, label, strings.ToLower(label), "case cannot carry information in DNS")
	for _, r := range label {
		ok := (r >= 'a' && r <= 'z') || (r >= '2' && r <= '7')
		assert.Assert(t, ok, "label %q has %q, which base32 does not produce", label, r)
	}
}

// A CID's leading bytes are the multihash prefix and codec — identical for every
// project made the same way. Hashing first is what stops the label being a constant.
func TestProjectLabelDoesNotInheritTheCidPrefix(t *testing.T) {
	a := schema.ProjectLabel("QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR")
	b := schema.ProjectLabel("QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxQ")
	assert.Assert(t, a[:4] != b[:4], "two CIDs sharing a prefix produced labels sharing one: %s %s", a, b)
}

// The length exists to make collisions cold. This does not prove the bound — it checks
// the encoding actually spends the entropy it claims to, which a truncation bug would
// break silently and which no other test would notice.
func TestProjectLabelsDoNotCollideAtScale(t *testing.T) {
	const n = 200_000
	seen := make(map[string]string, n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gV%08d", i)
		label := schema.ProjectLabel(id)
		if prev, dup := seen[label]; dup {
			t.Fatalf("collision at %d labels: %q from both %s and %s", len(seen), label, prev, id)
		}
		seen[label] = id
	}
}

func TestGeneratedHost(t *testing.T) {
	const id = "QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR"
	label := schema.ProjectLabel(id)

	assert.Equal(t, schema.GeneratedHost("db", id), "db."+label)
	// the apex of the project's own zone
	assert.Equal(t, schema.GeneratedHost("", id), label)
}
