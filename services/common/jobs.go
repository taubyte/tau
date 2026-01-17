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
	MockedPatrick          = false
	TimeoutTest            = false
	DefaultLockTime        = 600 * time.Second
	DefaultMaxTime         = 1 * time.Hour
	DefaultLockMinWaitTime = 30 * time.Second
	DefaultRefreshLockTime = 90 * time.Second
	DefaultTestTimeOut     = 5 * time.Second
)
