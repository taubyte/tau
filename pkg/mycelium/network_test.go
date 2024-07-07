package mycelium

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/taubyte/tau/pkg/mycelium/command"
	"github.com/taubyte/tau/pkg/mycelium/host"
)

type mockHost struct {
	mock.Mock
}

func (m *mockHost) Clone(attributes ...host.Attribute) (host.Host, error) {
	args := m.Called(attributes)
	return m, args.Error(1)
}

func (m *mockHost) Command(ctx context.Context, name string, options ...command.Option) (*command.Command, error) {
	args := m.Called(ctx, name, options)
	return args.Get(0).(*command.Command), args.Error(1)
}

func (m *mockHost) Fs(ctx context.Context, opts ...sftp.ClientOption) (afero.Fs, error) {
	args := m.Called(ctx, opts)
	return args.Get(0).(afero.Fs), args.Error(1)
}

func (m *mockHost) String() string {
	args := m.Called()
	return args.String(0)
}

func TestNetwork_AddWithCloneError(t *testing.T) {
	h1 := new(mockHost)
	h1.On("String").Return("host1")
	h1.On("Clone", mock.Anything).Return(nil, errors.New("clone error"))

	network, err := New()
	assert.NoError(t, err)

	err = network.Add(h1)
	assert.Error(t, err, "clone error")
}

func TestNetwork_AddWithOptions(t *testing.T) {
	option := func(n *Network) error {
		return nil
	}

	network, err := New(option)
	assert.NoError(t, err)
	assert.NotNil(t, network)
}

func TestNetwork_RunWithConcurrency1(t *testing.T) {
	h1 := new(mockHost)
	h1.On("String").Return("host1")
	h1.On("Clone", mock.Anything).Return(h1, nil)

	h2 := new(mockHost)
	h2.On("String").Return("host2")
	h2.On("Clone", mock.Anything).Return(h2, nil)

	network, err := New()
	assert.NoError(t, err)

	err = network.Add(h1, h2)
	assert.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	handler := func(h host.Host) error {
		if h.String() == "host2" {
			return errors.New("error on host2")
		}
		return nil
	}

	errs := network.Run(ctx, 1, handler)

	var errList []error
	for err := range errs {
		errList = append(errList, err)
	}

	assert.Len(t, errList, 1)
	assert.Equal(t, "error on host2", errList[0].Error())
}

func TestNetwork_HostsContextCancellation(t *testing.T) {
	h1 := new(mockHost)
	h1.On("String").Return("host1")
	h1.On("Clone", mock.Anything).Return(h1, nil)

	network, err := New()
	assert.NoError(t, err)

	err = network.Add(h1)
	assert.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	ch := network.Hosts(ctx)
	var hosts []host.Host
	for h := range ch {
		hosts = append(hosts, h)
	}

	assert.Len(t, hosts, 0)
}

func TestNetwork_RunContextCancellation(t *testing.T) {
	h1 := new(mockHost)
	h1.On("String").Return("host1")
	h1.On("Clone", mock.Anything).Return(h1, nil)

	h2 := new(mockHost)
	h2.On("String").Return("host2")
	h2.On("Clone", mock.Anything).Return(h2, nil)

	network, err := New()
	assert.NoError(t, err)

	err = network.Add(h1, h2)
	assert.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	handler := func(h host.Host) error {
		time.Sleep(2 * time.Second) // Simulate long processing
		return nil
	}

	errs := network.Run(ctx, 2, handler)

	var errList []error
	for err := range errs {
		errList = append(errList, err)
	}

	assert.Len(t, errList, 0)
}

func TestHost_String(t *testing.T) {
	h1 := new(mockHost)
	h1.On("String").Return("host1")

	assert.Equal(t, "host1", h1.String())
}

func TestHost_Clone(t *testing.T) {
	h1 := new(mockHost)
	h1.On("String").Return("host1")
	h1.On("Clone", mock.Anything).Return(h1, nil)

	network, err := New()
	assert.NoError(t, err)

	err = network.Add(h1)
	assert.NoError(t, err)

	clonedHost, err := h1.Clone()
	assert.NoError(t, err)
	assert.Equal(t, "host1", clonedHost.String())
}
