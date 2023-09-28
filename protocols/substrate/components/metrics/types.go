package metrics

/*
****** Encoding/Decoding ******
  - Append new metrics
  - Don't do: Type or order change will require new EncodingVersion version
*/

type Website struct {
	Cached float32
}

type Function struct {
	Cached     float32
	ColdStart  int64
	Memory     float64
	AvgRunTime int64
}

type Metric interface {
	Encode() []byte
	Decode(b []byte) error
	Less(Metric) bool
}
