package kvdb

import (
	"time"

	"github.com/taubyte/tau/pkg/fleet"
)

// RetentionOf is the T the cloud's fleet document says, 0 for the default. Unpublished
// is an answer: the cloud said nothing, and T is the default every node reaches the
// same way.
func RetentionOf(doc []byte, published bool) (time.Duration, error) {
	if !published {
		return 0, nil
	}
	d, err := fleet.Decode(doc)
	if err != nil {
		return 0, err
	}
	return time.Duration(d.Retention), nil
}

// DefaultRetention is the T a database writes under when nothing sets one.
func DefaultRetention() time.Duration { return DefaultOptions().Retention }
