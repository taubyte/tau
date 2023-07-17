package test

type servCounterMetrics struct {
	basePath                     string
	successCount                 int
	successTime                  float64
	successColdStartSuccessCount int
	successColdStartSuccessTime  float64
	successExecutionCount        int
	successExecutionTime         float64
	failCount                    int
	failTime                     float64
	failColdStartSuccessTime     float64
	failColdStartFailCount       int
	failColdStartFailTime        float64
	failExecutionCount           int
	failExecutionTime            float64
}

type structureDef struct {
	id   string
	fqdn string
	path string
}
