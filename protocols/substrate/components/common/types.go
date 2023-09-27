package common

type Metrics struct {
	Cached     float32 `cbor:"0,keyasint"`
	ColdStart  int64   `cbor:"1,keyasint"`
	Memory     float64 `cbor:"2,keyasint"`
	AvgRunTime int64   `cbor:"3,keyasint"`
}
