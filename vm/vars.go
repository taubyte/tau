package vm

import "time"

var (
	InstanceMaxRequests uint64 = 1024 * 64
	InstanceMaxError    uint64 = 10

	ShadowBuff                        = 10
	ShadowMaxAge        time.Duration = 10 * time.Minute
	ShadowCleanInterval time.Duration = ShadowMaxAge / 2
)
