package common

const ServiceName string = "patrick"
const Protocol string = "/patrick/v1"

type Location struct {
	Latitude  float32 `cbor:"1,keyasint"`
	Longitude float32 `cbor:"2,keyasint"`
}

type PeerLocation struct {
	Timestamp int64    `cbor:"1,keyasint"`
	Location  Location `cbor:"4,keyasint"`
}
