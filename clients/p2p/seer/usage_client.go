package seer

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/taubyte/tau/clients/p2p/seer/usage"
	iface "github.com/taubyte/tau/core/services/seer"
	"github.com/taubyte/tau/p2p/streams/command/response"
)

var (
	DefaultUsageBeaconInterval    = 30 * time.Second
	DefaultAnnounceBeaconInterval = 10 * time.Minute
	ErrorUsageBeaconStopped       = errors.New("usage Stopped")
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

func (u *Usage) updateUsage(hostname, nodeId, clientNodeId string, signature []byte) (response.Response, error) {
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

func (u *Usage) updateAnnounce(nodeId, clientNodeId string, signature []byte) (response.Response, error) {
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
			case <-u.ctx.Done():
				u.cleanStatus()
				u.status = ErrorUsageBeaconStopped
				return
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
