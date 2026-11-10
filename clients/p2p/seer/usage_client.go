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
	// AnnounceRetryInterval is the first wait after a failed announce; it
	// doubles on each further failure, up to DefaultAnnounceBeaconInterval.
	AnnounceRetryInterval   = 2 * time.Second
	ErrorUsageBeaconStopped = errors.New("usage Stopped")
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

	// get custom values
	if u.sensors != nil {
		for _, sensor := range u.sensors.List() {
			usageData.CustomValues[sensor.Name] = sensor.Value
		}
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
	// Usage beacon goroutine (includes initial call)
	go func() {
		// Initial usage update
		_, err := u.usage.updateUsage(u.hostname, u.nodeId, u.clientNodeId, u.signature)
		if err != nil {
			logger.Warnf("heartbeat to seer failed: %v", err)
			u._status <- err
		}

		// Periodic usage updates
		for {
			select {
			case <-u.ctx.Done():
				return
			case <-time.After(DefaultUsageBeaconInterval):
				_, err := u.usage.updateUsage(u.hostname, u.nodeId, u.clientNodeId, u.signature)
				if err != nil {
					u._status <- err
				}
			}
		}
	}()

	// Announce beacon goroutine. Seer answers DNS for a service only once it
	// has been announced, and at startup no seer may be reachable yet, so a
	// failed announce is retried soon rather than at the next refresh.
	go announceLoop(u.ctx, func() error {
		_, err := u.usage.updateAnnounce(u.nodeId, u.clientNodeId, u.signature)
		return err
	}, func(err error) { u._status <- err }, AnnounceRetryInterval, DefaultAnnounceBeaconInterval)

	// Status handler goroutine
	go func() {
		for {
			select {
			case <-u.ctx.Done():
				u.cleanStatus()
				u.status = ErrorUsageBeaconStopped
				return
			case err := <-u._status:
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

// announceLoop announces until ctx ends: every interval after a success, and
// after a failure first in retry, doubling up to interval. failed is told of
// each failure.
func announceLoop(ctx context.Context, announce func() error, failed func(error), retry, interval time.Duration) {
	next := retry
	for {
		wait := interval
		if err := announce(); err != nil {
			logger.Warnf("announce to seer failed, retrying in %s: %v", next, err)
			failed(err)
			wait, next = next, min(next*2, interval)
		} else {
			next = retry
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}
