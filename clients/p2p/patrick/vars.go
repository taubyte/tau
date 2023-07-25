package patrick

import (
	"errors"
	"time"

	"github.com/ipfs/go-log/v2"
)

var (
	MinPeers                 = 0
	MaxPeers                 = 5
	DefaultGeoBeaconInterval = 5 * time.Minute
	ErrorGeoBeaconStoped     = errors.New("GeoBeacon Stopped")
	logger                   = log.Logger("patrick.p2p.client")
)
