package metrics

import (
	"fmt"

	"github.com/taubyte/go-interfaces/services/substrate"
	"golang.org/x/exp/constraints"
)

type sum[T constraints.Integer | constraints.Float] struct {
	singleNumber[T]
}

func NewSumMetric[T constraints.Integer | constraints.Float](val T) substrate.Metric {
	m := &sum[T]{}
	m.Set(val)
	return m
}

func (m *sum[T]) Aggregate(metric substrate.Metric) error {
	_m, ok := metric.(*sum[T])
	if !ok {
		return fmt.Errorf("metrics are not the same type")
	}

	m.Set(_m.Number() + m.Number())
	return nil
}

func (m *sum[T]) Interface() interface{} {
	return m.Number()
}
