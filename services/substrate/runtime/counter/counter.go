package counter

import (
	"path"
	"time"

	"github.com/taubyte/tau/core/services/substrate/components"
	"github.com/taubyte/tau/core/services/substrate/counters"
	"github.com/taubyte/tau/services/substrate/components/counters/metrics"
)

// ErrorWrapper is an wraps an error in the cold start and execution of a serviceable.
// It handles the counter reporting for a serviceable based on its success and failures.
//
// To use the startTime should always be the time the cold start began, in most cases this is when the matcher has been created/when a lookup request has been made.
// The coldStartDoneTime is always the time the cold start has been successfully completed. If there is an error
// during the cold start an empty time.Time (time.Time{}) should be used.
// The error is an error in either cold start, or execution.
//
// If a cold start time is provided, and an error the counter that will be pushed is a successful cold start, with a failed execution.
// If a cold start time is provided, and a nil error the counter that will be pushed is a successful cold start and execution.
func ErrorWrapper(serviceable components.Serviceable, startTime time.Time, coldStartDone time.Time, gerr error) error {
	if serviceable.Service().Counter().Implemented() {
		go func() {
			if serviceable != nil {
				doneTime := time.Now()
				var skipExecution bool
				basePath := counters.NewPath(path.Join(serviceable.Project(), serviceable.Id()))
				totalTime := doneTime.Sub(startTime).Nanoseconds()

				if gerr != nil {
					basePath = basePath.Failed()
				} else {
					basePath = basePath.Success()
				}

				var coldStartTime int64
				coldStartPath := basePath.ColdStart()
				if coldStartDone.IsZero() {
					skipExecution = true
					coldStartPath = coldStartPath.Failed()
					coldStartTime = totalTime
				} else {
					coldStartPath = coldStartPath.Success()
					coldStartTime = coldStartDone.Sub(startTime).Nanoseconds()
				}

				ws := []*counters.WrappedMetric{
					{
						Key:    basePath.String(),
						Metric: metrics.NewSumMetric[uint64](1),
					},
					{
						Key:    basePath.Time().String(),
						Metric: metrics.NewSumMetric(totalTime),
					},
					{
						Key:    coldStartPath.String(),
						Metric: metrics.NewSumMetric[uint64](1),
					},
					{
						Key:    coldStartPath.Time().String(),
						Metric: metrics.NewSumMetric(coldStartTime),
					},
				}
				if !skipExecution {
					ws = append(ws,
						&counters.WrappedMetric{
							Key:    basePath.Execution().String(),
							Metric: metrics.NewSumMetric[uint64](1),
						},
						&counters.WrappedMetric{
							Key:    basePath.Execution().Time().String(),
							Metric: metrics.NewSumMetric(doneTime.Sub(coldStartDone).Nanoseconds()),
						},
					)
				}
				serviceable.Service().Counter().Push(ws...)
			}
		}()
	}

	return gerr
}
