package common

import "time"

const (
	ServiceName string = "tns"
)

var (
	UpdateBroadcastTimeout    time.Duration = 3 * time.Second
	UpdateBroadcastRetryPause time.Duration = 100 * time.Millisecond
)
