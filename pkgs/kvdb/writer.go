package kvdb

import "time"

var MaxTimeBetweenGroupedWrites = 5 * time.Millisecond

// TODO: implement a mechanism to group PUT operations
