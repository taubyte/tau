package database

import "time"

const defaultRebroadcastIntervalSec int = 5

var (
	QueryBufferSize        = 1024
	ReadQueryResultTimeout = 50 * time.Millisecond
)
