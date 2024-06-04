package report

import (
	"time"

	"github.com/taubyte/tau/core/services/substrate/counters"
)

type Report struct {
	Success SuccessReport `json:",omitempty"`
	Failure FailureReport `json:",omitempty"`
}

type ReportMetric struct {
	Count uint64        `json:",omitempty"`
	Time  time.Duration `json:",omitempty"`
}

type SuccessReport struct {
	ReportMetric `json:",omitempty"`
	ColdStart    ReportMetric `json:",omitempty"`
	Execution    ReportMetric `json:",omitempty"`
}

type FailureReport struct {
	ReportMetric     `json:",omitempty"`
	ColdStartSuccess ReportMetric `json:",omitempty"`
	ColdStartFailure ReportMetric `json:",omitempty"`
	ExecutionFailure ReportMetric `json:",omitempty"`
}

type MetricMap map[string]counters.Metric

type metricVal struct {
	val interface{}
}
