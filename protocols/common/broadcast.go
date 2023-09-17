package common

import "time"

var (
	UpdateBroadcastTimeout    time.Duration = 3 * time.Second // Timeout is set to 3 seconds
	UpdateBroadcastRetryPause time.Duration = 100 * time.Millisecond // 100 millisecond pause between each retry attempt
)
