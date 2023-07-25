package metrics

import (
	"fmt"

	"github.com/taubyte/go-interfaces/services/substrate"
	"golang.org/x/exp/constraints"
)

type max[T constraints.Integer | constraints.Float] struct {
	singleNumber[T]
}

func NewMaxMetric[T constraints.Integer | constraints.Float](val T) substrate.Metric {
	m := &max[T]{}
	m.Set(val)
	return m
}

func (m *max[T]) Aggregate(metric substrate.Metric) error {
	_m, ok := metric.(*max[T])
	if !ok {
		return fmt.Errorf("metrics are not the same type")
	}

	if _m.Number() > m.Number() {
		m.Set(_m.Number())
	}
	return nil
}

func (m *max[T]) Interface() interface{} {
	return m.Number()
}
