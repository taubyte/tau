package node

import (
	"context"
	"errors"

	"github.com/taubyte/tau/pkg/stats"
	v2proxy "github.com/taubyte/tau/services/v2/proxy"
	"github.com/taubyte/tau/services/worker"
)

// proxy.go runs the L4 listeners for the proxies this node holds.
//
// # It needs a worker, and that is the whole of what it needs
//
// What a node can serve is what it is already serving — the register does not
// enumerate — so the list of proxies comes from the worker's held deployments,
// and the compiled projects come from the cache that worker already keeps. A
// second cache of the same objects could disagree with it about which revision
// is current, which for a listener means forwarding to a workload the config no
// longer names.
//
// Where the connection goes is placement's question, exactly as it is for the
// gateway: which node answers is decided from live measurements, and half of
// that decision here would be half of it in two places.
//
// # Nothing here names an address or a port
//
// Both are the service's own business — it listens on every address this node
// has and the kernel gives it a port. What this file adds is the report: the
// names those listeners answer on go into Serving so an A record resolves, and
// the sockets go into Sockets so an SRV record carries the port. See
// services/dns, which is the only way a client ever learns it.

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

	svc, err := v2proxy.New(n.cfg.Proxy,
		v2proxy.WithProjects(n.worker),
		v2proxy.WithWorkers(n.workers),
		v2proxy.WithDeployments(&proxies{node: n}),
		v2proxy.WithWarn(func(format string, args ...any) { logger.Warnf(format, args...) }),
	)
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

// interface check: the worker is what the proxy reads deployments through.
var _ v2proxy.Projects = (*worker.Service)(nil)
