package metrics

import "golang.org/x/exp/constraints"

type singleNumber[T constraints.Integer | constraints.Float] struct {
	Value T
}

func (s *singleNumber[T]) Number() T {
	return s.Value
}

func (s *singleNumber[T]) Set(value T) {
	s.Value = value
}

func (s *singleNumber[T]) Reset() {
	s.Set(0)
}

func (s *singleNumber[T]) Interface() interface{} {
	return s.Value
}
