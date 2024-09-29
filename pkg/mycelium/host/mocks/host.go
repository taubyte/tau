package mocks

import (
	"context"

	"github.com/pkg/sftp"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/mock"
	"github.com/taubyte/tau/pkg/mycelium/command"
	"github.com/taubyte/tau/pkg/mycelium/host"
)

type Host struct {
	mock.Mock
}

func (m *Host) Clone(attributes ...host.Attribute) (host.Host, error) {
	args := m.Called(attributes)
	return m, args.Error(1)
}

func (m *Host) Command(ctx context.Context, name string, options ...command.Option) (*command.Command, error) {
	args := m.Called(ctx, name, options)
	return args.Get(0).(*command.Command), args.Error(1)
}

func (m *Host) Fs(ctx context.Context, opts ...sftp.ClientOption) (afero.Fs, error) {
	args := m.Called(ctx, opts)
	return args.Get(0).(afero.Fs), args.Error(1)
}

func (m *Host) String() string {
	args := m.Called()
	return args.String(0)
}

func (m *Host) Name() string {
	args := m.Called()
	return args.String(0)
}

func (m *Host) Tags() []string {
	args := m.Called()
	return args.Get(0).([]string)
}
