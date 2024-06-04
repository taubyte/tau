package counters

import goPath "path"

type path string

type Path interface {
	ColdStart() path
	Execution() path
	FailColdStartMetricPaths() (successCountPath string, successTimePath string, failCountPath string, failTimePath string)
	FailExecutionMetricPaths() (countPath string, timePath string)
	FailMetricPaths() (countPath string, timePath string)
	Failed() path
	Join(toJoin string) path
	Memory() path
	SmartOp(smartOpId string) path
	String() string
	Success() path
	SuccessColdStartMetricPaths() (countPath string, timePath string)
	SuccessExecutionMetricPaths() (countPath string, timePath string)
	SuccessMetricPaths() (countPath string, timePath string)
	Time() path
}

func NewPath(basePath string) path {
	return path(basePath)
}

func (c path) String() string {
	return string(c)
}

func join[v string | path](basePath Path, toJoin v) path {
	return NewPath(goPath.Join(basePath.String(), string(toJoin)))
}

func (c path) Join(toJoin string) path {
	return join(c, toJoin)
}

func (c path) Failed() path {
	return join(c, failed)
}

func (c path) Success() path {
	return join(c, success)
}

func (c path) Time() path {
	return join(c, time)
}

func (c path) Memory() path {
	return join(c, memory)
}

func (c path) Execution() path {
	return join(c, execution)
}

func (c path) ColdStart() path {
	return join(c, coldStart)
}

func (c path) SmartOp(smartOpId string) path {
	return c.Join(smartOpId).Join(smartOpId)
}

/***************************** Common Paths For Getting From Database ****************************/

func (c path) SuccessMetricPaths() (countPath, timePath string) {
	return c.Success().String(), c.Success().Time().String()
}

func (c path) SuccessColdStartMetricPaths() (countPath, timePath string) {
	return c.Success().ColdStart().Success().String(), c.Success().ColdStart().Success().Time().String()
}

func (c path) SuccessExecutionMetricPaths() (countPath, timePath string) {
	return c.Success().Execution().String(), c.Success().Execution().Time().String()
}

func (c path) FailMetricPaths() (countPath, timePath string) {
	return c.Failed().String(), c.Failed().Time().String()
}

func (c path) FailColdStartMetricPaths() (successCountPath, successTimePath, failCountPath, failTimePath string) {
	return c.Failed().ColdStart().Success().String(),
		c.Failed().ColdStart().Success().Time().String(),
		c.Failed().ColdStart().Failed().String(),
		c.Failed().ColdStart().Failed().Time().String()
}

func (c path) FailExecutionMetricPaths() (countPath, timePath string) {
	return c.Failed().Execution().String(), c.Failed().Execution().Time().String()
}
