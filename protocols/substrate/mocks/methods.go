package mocks

import (
	"context"

	"github.com/pkg/errors"
	"github.com/taubyte/go-interfaces/services/substrate"
	"github.com/taubyte/go-interfaces/services/tns"
	"github.com/taubyte/go-interfaces/vm"
	httpSrv "github.com/taubyte/http"
	"github.com/taubyte/p2p/peer"
)

func (m *mockedSubstrate) Node() peer.Node {
	return m.node
}

func (m *mockedSubstrate) Close() error {
	err := m.vm.Close()
	if err0 := m.smartOps.Close(); err0 != nil {
		if err != nil {
			err = errors.New(err.Error() + ":::" + err0.Error())
		} else {
			err = err0
		}
	}

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

func (m *mockedSubstrate) SmartOps() substrate.SmartOpsService {
	return m.smartOps
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
