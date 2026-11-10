package core

import (
	"crypto/rand"
	"time"
)

// NewContainerID names a new container. The time is for people reading it; the
// random part is what keeps containers created in the same instant apart.
func NewContainerID() ContainerID {
	return ContainerID("tau-" + time.Now().Format("20060102-150405") + "-" + rand.Text())
}
