package common

import (
	moody "bitbucket.org/taubyte/go-moody-blues"
)

var RetryErrorString = "Retry this job"
var LocalPatrick = false
var TimeoutTest = false
var DefaultLockTime = 300
var DefaultTestTimeOut = 5
var Logger, _ = moody.New("monkey.service")
