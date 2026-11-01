package node

import (
	"context"
	"errors"

	"github.com/taubyte/tau/pkg/stats"
	v2proxy "github.com/taubyte/tau/services/proxy"
)

// Runs the L4 listeners for the proxies this node holds, listed from the worker's
// held deployments and read from its cache, so there is one view of the current
// revision. Where a connection goes is placement's. The service picks addresses and
// ports; this reports the names in Serving (A records) and sockets in Sockets (SRV),
// read by services/dns.

// proxies adapts the worker's view of what this node holds to what the proxy
// service takes.
type proxies struct{ node *Node }

func (p *proxies) Proxies(ctx context.Context) []v2proxy.Bind {
	if p.node.worker == nil {
		return nil
	}

	held := p.node.worker.Proxies(ctx)
	out := make([]v2proxy.Bind, 0, len(held))
	for _, h := range held {
		out = append(out, v2proxy.Bind{
			Project:     h.Project,
			Environment: h.Environment,
			Scope:       h.Scope,
			Name:        h.Name,
		})
	}
	return out
}

func (n *Node) openProxy() error {
	if !n.cfg.runs(Proxy) {
		return nil
	}
	if n.worker == nil {
		return errors.New("node: a proxy serves the deployments a worker holds, and this node runs none")
	}
	if n.workers == nil {
		return errors.New("node: a proxy reaches a workload through placement, and this node has none")
	}

	options := []v2proxy.Option{
		v2proxy.WithProjects(n.worker),
		v2proxy.WithWorkers(n.workers),
		v2proxy.WithDeployments(&proxies{node: n}),
		v2proxy.WithWarn(func(format string, args ...any) { logger.Warnf(format, args...) }),
	}

	// The same secret resolver the worker reads: a terminated certificate is a
	// deployment secret; without one, `tls: terminate` is served only where the cloud's
	// wildcard covers the name. openProxy runs after openWorker.
	switch {
	case n.opts.secrets != nil:
		options = append(options, v2proxy.WithSecrets(n.opts.secrets))
	case n.vault != nil:
		options = append(options, v2proxy.WithSecrets(n.vault))
	}

	svc, err := v2proxy.New(n.cfg.Proxy, options...)
	if err != nil {
		return err
	}

	n.proxy = svc
	n.closers = append(n.closers, svc)
	return nil
}

// proxySockets is what this node's listeners answer on, for its own report.
func (n *Node) proxySockets() []stats.Socket {
	if n.proxy == nil {
		return nil
	}

	serving := n.proxy.Serving()
	out := make([]stats.Socket, 0, len(serving))
	for _, s := range serving {
		out = append(out, stats.Socket{
			Host:      s.Host,
			Protocol:  s.Protocol,
			Transport: s.Transport,
			Port:      s.Port,
		})
	}
	return out
}
