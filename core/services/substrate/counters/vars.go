package counters

import goTime "time"

var (
	DefaultReportTime = 5 * goTime.Minute
)

const (
	time   = "t"
	memory = "m"

	failed  = "f"
	success = "s"

	coldStart = "cs"
	execution = "e"
)
