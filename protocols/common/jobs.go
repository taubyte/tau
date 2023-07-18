package common

import "time"

var (
	// From Patrick
	FakeSecret        = false
	DelayJob          = false
	RetryJob          = false
	DelayJobTime      = 60 * time.Second
	MaxCancelAttempts = 30
	MaxJobAttempts    = 2

	// From Monkey
	RetryErrorString   = "Retry this job"
	LocalPatrick       = false
	TimeoutTest        = false
	DefaultLockTime    = 300
	DefaultTestTimeOut = 5
)
