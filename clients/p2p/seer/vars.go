package seer

import (
	"errors"
	"time"

	"github.com/ipfs/go-log/v2"
)

var (
	DefaultGeoBeaconInterval = 5 * time.Minute
	ErrorGeoBeaconStopped    = errors.New("geoBeacon Stopped")
	logger                   = log.Logger("seer.p2p.client")
)
