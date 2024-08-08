package mycelium

import (
	"context"
	"fmt"

	"github.com/taubyte/tau/pkg/mycelium/host"
)

type Network struct {
	hosts map[string]host.Host
	auth  []host.Attribute
}

type Option func(*Network) error

func New(options ...Option) (*Network, error) {
	n := &Network{
		hosts: make(map[string]host.Host),
	}

	for _, opt := range options {
		if err := opt(n); err != nil {
			return nil, err
		}
	}

	return n, nil
}

func (n *Network) Add(hosts ...host.Host) error {
	for _, h := range hosts {
		if _, exists := n.hosts[h.String()]; exists {
			return fmt.Errorf("host `%s` already exists", h.String())
		}

		hc, err := h.Clone(n.auth...)
		if err != nil {
			return err
		}

		n.hosts[hc.String()] = hc
	}

	return nil
}

func (n *Network) Hosts(ctx context.Context) chan host.Host {
	ch := make(chan host.Host, 64)

	go func() {
		defer close(ch)
		for _, h := range n.hosts {
			select {
			case <-ctx.Done():
				return
			default:
				ch <- h
			}
		}
	}()

	return ch
}

// to stop at first error set concurrency=1 and stop at first error
func (n *Network) Run(ctx context.Context, concurrency uint16, handler func(host.Host) error) chan error {
	ch := make(chan error, concurrency)

	go func() {
		defer close(ch)
		sem := make(chan struct{}, concurrency)

		for _, h := range n.hosts {
			select {
			case <-ctx.Done():
				return
			case sem <- struct{}{}:
				go func(host host.Host) {
					defer func() { <-sem }()
					if err := handler(host); err != nil {
						select {
						case <-ctx.Done():
							return
						case ch <- err:
						}
					}
				}(h)
			}
		}

		// Wait for all goroutines to finish
		for i := 0; i < cap(sem); i++ {
			sem <- struct{}{}
		}
	}()

	return ch
}
