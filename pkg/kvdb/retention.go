package kvdb

import "time"

// DefaultRetention is the T a database writes under when nothing sets one.
func DefaultRetention() time.Duration { return DefaultOptions().Retention }
