package counters

type Metric interface {
	Reset()
	Aggregate(Metric) error
	Interface() interface{}
}

type WrappedMetric struct {
	Key    string
	Metric Metric
}
