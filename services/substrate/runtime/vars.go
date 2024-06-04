package runtime

import "time"

var (
	InstanceMaxRequests   int           = 1024 * 64
	InstanceMaxError      int64         = 10
	InstanceErrorCoolDown time.Duration = 30 * time.Minute

	ShadowBuff                        = 10
	ShadowMaxAge        time.Duration = 10 * time.Minute
	ShadowCleanInterval time.Duration = ShadowMaxAge / 2
)
