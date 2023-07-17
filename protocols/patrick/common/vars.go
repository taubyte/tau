package common

import "time"

var FakeSecret = false
var DelayJob = false
var RetryJob = false
var DelayJobTime = 60 * time.Second
var MaxCancelAttemps = 30
var MaxJobAttempts = 2
