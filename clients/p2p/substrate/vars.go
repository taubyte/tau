package substrate

import "time"

var (
	DefaultTimeOut   = 10 * time.Millisecond
	DefaultThreshold = 3
)

const (
	CommandHTTP = "proxy-http"

	BodyHost   = "host"
	BodyPath   = "path"
	BodyMethod = "method"

	ResponseCached     = "cached"
	ResponseAverageRun = "average-run"
	ResponseColdStart  = "cold-start"
	ResponseMemory     = "memory"
	ResponseCpuCount   = "cpus"
	ResponseAverageCpu = "average-cpu"
)
