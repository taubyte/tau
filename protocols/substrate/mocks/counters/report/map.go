package report

func (m MetricMap) value(_path string) metricVal {
	if v, ok := m[_path]; ok {
		return metricVal{v.Interface()}
	}

	return metricVal{}
}
