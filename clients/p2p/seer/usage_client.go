package p2p

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/taubyte/go-interfaces/p2p/streams"
	iface "github.com/taubyte/go-interfaces/services/seer"
	"github.com/taubyte/odo/clients/p2p/seer/usage"
)

var (
	DefaultUsageBeaconInterval    = 30 * time.Second
	DefaultAnnounceBeaconInterval = 10 * time.Minute
	DefaultUsageBeaconStopedError = errors.New("Usage Stopped")
)

type Usage Client

type UsageBeacon struct {
	ctx        context.Context
	ctx_cancel context.CancelFunc
	usage      *Usage
	hostname   string
	status     error
	_status    chan error

	nodeId       string
	clientNodeId string
	signature    []byte
}

func (c *Client) Usage() iface.Usage {
	return (*Usage)(c)
}

func (u *Usage) AddService(svrType iface.ServiceType, meta map[string]string) {
	if meta == nil {
		meta = make(map[string]string)
	}
	u.services = append(u.services, iface.ServiceInfo{Type: svrType, Meta: meta})
}

func (u *Usage) updateUsage(hostname, nodeId, clientNodeId string, signature []byte) (streams.Response, error) {
	usageData, err := usage.GetUsage()
	if err != nil {
		return nil, fmt.Errorf("getting usage of hostname `%s` failed with: %s", hostname, err)
	}

	resp, err := u.Heartbeat(&usageData, hostname, nodeId, clientNodeId, signature)
	if err != nil {
		return nil, err
	}

	return resp, nil
}

func (u *Usage) updateAnnounce(nodeId, clientNodeId string, signature []byte) (streams.Response, error) {
	resp, err := u.Announce(u.services, nodeId, clientNodeId, signature)
	if err != nil {
		return nil, err
	}

	return resp, nil
}

func (u *UsageBeacon) cleanStatus() {
	for {
		select {
		case <-u._status:
		default:
			return
		}
	}
}

func (u *Usage) Beacon(hostname, nodeId, clientNodeId string, signature []byte) iface.UsageBeacon {
	ctx, ctx_cancel := context.WithCancel(u.client.Context())
	return &UsageBeacon{
		ctx:        ctx,
		hostname:   hostname,
		ctx_cancel: ctx_cancel,
		usage:      u,
		_status:    make(chan error, 16),

		nodeId:       nodeId,
		clientNodeId: clientNodeId,
		signature:    signature,
	}
}

func (u *UsageBeacon) Start() {
	go func() {
		var err error

		// First update as soon as we start
		time.Sleep(3 * time.Second)

		_, err = u.usage.updateUsage(u.hostname, u.nodeId, u.clientNodeId, u.signature)
		if err != nil {
			u._status <- err
		}

		_, err = u.usage.updateAnnounce(u.nodeId, u.clientNodeId, u.signature)
		if err != nil {
			u._status <- err
		}

		for {
			select {
			case <-time.After(DefaultAnnounceBeaconInterval):
				_, err := u.usage.updateAnnounce(u.nodeId, u.clientNodeId, u.signature)
				if err != nil {
					u._status <- err
				}
			case <-time.After(DefaultUsageBeaconInterval):
				_, err := u.usage.updateUsage(u.hostname, u.nodeId, u.clientNodeId, u.signature)
				if err != nil {
					u._status <- err
				}
			case err = <-u._status:
				u.status = err
			case <-u.ctx.Done():
				u.cleanStatus()
				u.status = DefaultUsageBeaconStopedError
				return
			}
		}
	}()
}

func (u *UsageBeacon) Status() error {
	return u.status
}

func (u *UsageBeacon) Stop() {
	u.ctx_cancel()
}
