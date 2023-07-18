package common

import "time"

var (
	UpdateBroadcastTimeout    time.Duration = 3 * time.Second
	UpdateBroadcastRetryPause time.Duration = 100 * time.Millisecond
)
