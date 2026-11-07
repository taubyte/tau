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
	DefaultLockTime        = 60 * time.Second // a claim not refreshed for this long goes back on the queue
	DefaultMaxTime         = 1 * time.Hour
	DefaultRefreshLockTime = 20 * time.Second // how often a building Monkey refreshes its claim
	DefaultTestTimeOut     = 5 * time.Second
)
