package mocks

import (
	"context"

	"github.com/taubyte/tau/core/services/substrate"
	"github.com/taubyte/tau/core/services/tns"
	"github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/p2p/peer"
	httpSrv "github.com/taubyte/tau/pkg/http"
)

func (m *mockedSubstrate) Node() peer.Node {
	return m.node
}

func (m *mockedSubstrate) Close() error {
	err := m.vm.Close()
	m.ctxC()
	return err
}

func (m *mockedSubstrate) Http() httpSrv.Service {
	return m.http
}

func (m *mockedSubstrate) Vm() vm.Service {
	return m.vm
}

func (m *mockedSubstrate) Tns() tns.Client {
	return m.tns
}

func (m *mockedSubstrate) Counter() substrate.CounterService {
	return m.counters
}

// TODO: Add functionality to attach plugins
func (m *mockedSubstrate) Orbitals() []vm.Plugin {
	return nil
}

func (m *mockedSubstrate) Context() context.Context {
	return m.ctx
}

func (m *mockedSubstrate) Dev() bool {
	return true
}

func (m *mockedSubstrate) Verbose() bool {
	return true
}
