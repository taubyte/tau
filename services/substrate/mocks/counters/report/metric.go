package report

import "time"

func (m metricVal) uint() (val uint64) {
	if m.val != nil {
		val = m.val.(uint64)
	}

	return val
}

func (m metricVal) duration() (dur time.Duration) {
	if m.val != nil {
		val := m.val.(int64)
		dur = time.Duration(val) * time.Nanosecond
	}

	return
}
