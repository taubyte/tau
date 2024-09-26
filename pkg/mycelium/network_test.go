package mycelium

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/taubyte/tau/pkg/mycelium/host"
	"github.com/taubyte/tau/pkg/mycelium/host/mocks"
)

func TestNetwork_AddWithCloneError(t *testing.T) {
	h1 := new(mocks.Host)
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
	h1 := new(mocks.Host)
	h1.On("String").Return("host1")
	h1.On("Clone", mock.Anything).Return(h1, nil)

	h2 := new(mocks.Host)
	h2.On("String").Return("host2")
	h2.On("Clone", mock.Anything).Return(h2, nil)

	network, err := New()
	assert.NoError(t, err)

	err = network.Add(h1, h2)
	assert.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	handler := func(ctx context.Context, h host.Host) error {
		if h.String() == "host2" {
			return errors.New("error on host2")
		}
		return nil
	}

	errs := network.Run(ctx, 1, handler)

	var errList []error
	for err := range errs {
		errList = append(errList, err.Error)
	}

	assert.Len(t, errList, 1)
	assert.Equal(t, "error on host2", errList[0].Error())
}

func TestNetwork_HostsContextCancellation(t *testing.T) {
	h1 := new(mocks.Host)
	h1.On("String").Return("host1")
	h1.On("Clone", mock.Anything).Return(h1, nil)

	network, err := New()
	assert.NoError(t, err)

	err = network.Add(h1)
	assert.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	ch := network.StreamHosts(ctx)
	var hosts []host.Host
	for h := range ch {
		hosts = append(hosts, h)
	}

	assert.Len(t, hosts, 0)
}

func TestNetwork_RunContextCancellation(t *testing.T) {
	h1 := new(mocks.Host)
	h1.On("String").Return("host1")
	h1.On("Clone", mock.Anything).Return(h1, nil)

	h2 := new(mocks.Host)
	h2.On("String").Return("host2")
	h2.On("Clone", mock.Anything).Return(h2, nil)

	network, err := New()
	assert.NoError(t, err)

	err = network.Add(h1, h2)
	assert.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	handler := func(ctx context.Context, h host.Host) error {
		time.Sleep(2 * time.Second) // Simulate long processing
		return nil
	}

	errs := network.Run(ctx, 2, handler)

	var errList []error
	for err := range errs {
		errList = append(errList, err.Error)
	}

	assert.Len(t, errList, 0)
}

func TestHost_String(t *testing.T) {
	h1 := new(mocks.Host)
	h1.On("String").Return("host1")

	assert.Equal(t, "host1", h1.String())
}

func TestHost_Clone(t *testing.T) {
	h1 := new(mocks.Host)
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
