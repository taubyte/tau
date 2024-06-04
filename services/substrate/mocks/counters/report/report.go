package report

import (
	"encoding/json"
	"path"
	"time"

	"github.com/taubyte/tau/core/services/substrate/counters"
)

func (m MetricMap) Report(projectId string, resourceId string) Report {
	basePath := counters.NewPath(path.Join(projectId, resourceId))

	sC, sT := basePath.SuccessMetricPaths()
	sCsC, sCsT := basePath.SuccessColdStartMetricPaths()
	sEC, sET := basePath.SuccessExecutionMetricPaths()

	fC, fT := basePath.FailMetricPaths()
	fCsSC, fCsST, fCsFC, fCsFT := basePath.FailColdStartMetricPaths()
	fEC, fET := basePath.FailExecutionMetricPaths()

	report := Report{}
	report.Success.Count = m.value(sC).uint()
	report.Success.Time = m.value(sT).duration()
	report.Success.ColdStart.Count = m.value(sCsC).uint()
	report.Success.ColdStart.Time = m.value(sCsT).duration()
	report.Success.Execution.Count = m.value(sEC).uint()
	report.Success.Execution.Time = m.value(sET).duration()

	report.Failure.Count = m.value(fC).uint()
	report.Failure.Time = m.value(fT).duration()
	report.Failure.ColdStartSuccess.Count = m.value(fCsSC).uint()
	report.Failure.ColdStartSuccess.Time = m.value(fCsST).duration()
	report.Failure.ColdStartFailure.Count = m.value(fCsFC).uint()
	report.Failure.ColdStartFailure.Time = m.value(fCsFT).duration()
	report.Failure.ExecutionFailure.Count = m.value(fEC).uint()
	report.Failure.ExecutionFailure.Time = m.value(fET).duration()

	return report
}

func (r Report) String() string {
	data, err := json.Marshal(r)
	if err != nil {
		return ""
	}

	return string(data)
}

func (r ReportMetric) Average() time.Duration {
	return r.Time / time.Duration(r.Count)
}
