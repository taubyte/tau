package runtime

import "time"

var (
	InstanceMaxRequests   int           = 1024 * 64
	InstanceMaxError      int64         = 5
	InstanceErrorCoolDown time.Duration = 30 * time.Minute

	ShadowMinBuff                     = 3
	ShadowMaxAge        time.Duration = 10 * time.Minute
	ShadowCleanInterval time.Duration = ShadowMaxAge / 2

	ShadowMaxWait time.Duration = 1 * time.Second

	MaxGlobalInstances int64  = 128 * 1024
	MemoryThreshold    uint64 = 80

	DefaultWasmMemory uint64 = 4 * 1024 * 1024 * 1024
)
