package structure

import (
	"github.com/taubyte/go-interfaces/services/substrate"
)

var _ substrate.CounterService = &TestCounters{}

type TestCounters struct {
	substrate.Service
}

func (tc *TestCounters) Push(...*substrate.WrappedMetric) {}
