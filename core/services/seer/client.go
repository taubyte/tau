package seer

type Geo interface {
	All() ([]*Peer, error)
	Set(location Location) error
	Beacon(location Location) GeoBeacon
	Distance(from Location, distance float32) ([]*Peer, error)
}

type GeoBeacon interface {
	Start()
}

type Client interface {
	Geo() Geo
	Usage() Usage
}
