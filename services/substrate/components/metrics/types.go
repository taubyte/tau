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
	// Free is the share of the node's in-flight limit still open, 0 to 1.
	Free float32
}

type Iface interface {
	Encode() []byte
	Decode(b []byte) error
	Less(Iface) bool
}
