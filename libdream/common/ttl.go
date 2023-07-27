package common

import (
	"math/rand"
	"time"
)

var (
	BaseAfterStartDelay = 100 // Millisecond
	MaxAfterStartDelay  = 500 // Millisecond
	MeshTimeout         = 5 * time.Second
)

func AfterStartDelay() time.Duration {
	rand.Seed(time.Now().UnixNano())
	return time.Duration(BaseAfterStartDelay+rand.Intn(MaxAfterStartDelay-BaseAfterStartDelay)) * time.Millisecond
}
