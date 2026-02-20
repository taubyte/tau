package sensors_test

import (
	"context"
	"fmt"
	"math"
	"net"
	"testing"
	"time"

	"connectrpc.com/connect"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/require"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/pkg/sensors"
	sensorsv1 "github.com/taubyte/tau/pkg/sensors/proto/gen/sensors/v1"
)

type mockNode struct {
	peer.Node
	id  libp2ppeer.ID
	ctx context.Context
}

func (m *mockNode) ID() libp2ppeer.ID {
	return m.id
}

func (m *mockNode) Context() context.Context {
	if m.ctx == nil {
		return context.Background()
	}
	return m.ctx
}

func newTestService(t *testing.T, node *mockNode, registry *sensors.Registry) *sensors.Service {
	opts := []sensors.Option{sensors.WithPort(0)}
	if registry != nil {
		opts = append(opts, sensors.WithRegistry(registry))
	}
	svc, err := sensors.New(node, opts...)
	require.NoError(t, err)
	return svc
}

func TestAll(t *testing.T) {
	// Allow time for default port (4217) to be released from previous runs before any subtest.
	time.Sleep(2 * time.Second)

	tests := []struct {
		name string
		fn   func(t *testing.T)
	}{
		// New_DefaultPort runs first so it can bind to 4217 before any other test.
		{"New_DefaultPort", func(t *testing.T) {
			testID, _ := libp2ppeer.Decode("12D3KooWMn5qZpfJckxXBgRd4syQMhkkzbAFnjwPFzAJByj5vLLn")
			mockNode := &mockNode{id: testID}

			svc, err := sensors.New(mockNode)
			require.NoError(t, err)
			require.NotNil(t, svc)

			addr := svc.Addr().(*net.TCPAddr)
			require.Equal(t, sensors.DefaultPort, addr.Port)
		}},
		{"Service_PushValue", func(t *testing.T) {
			testID, _ := libp2ppeer.Decode("12D3KooWMn5qZpfJckxXBgRd4syQMhkkzbAFnjwPFzAJByj5vLLn")
			mockNode := &mockNode{id: testID}
			service := newTestService(t, mockNode, nil)
			ctx := context.Background()

			_, err := service.PushValue(ctx, connect.NewRequest(&sensorsv1.PushValueRequest{
				Name:      "cpu",
				Value:     42.5,
				Timestamp: time.Now().Unix(),
			}))
			require.NoError(t, err)

			value, ok, err := service.Registry().Get("cpu")
			require.NoError(t, err)
			require.True(t, ok)
			require.Equal(t, 42.5, value)
		}},
		{"Service_PushValue_Validation", func(t *testing.T) {
			testID, _ := libp2ppeer.Decode("12D3KooWMn5qZpfJckxXBgRd4syQMhkkzbAFnjwPFzAJByj5vLLn")
			mockNode := &mockNode{id: testID}
			service := newTestService(t, mockNode, nil)
			ctx := context.Background()

			_, err := service.PushValue(ctx, connect.NewRequest(&sensorsv1.PushValueRequest{
				Name:      "",
				Value:     10,
				Timestamp: time.Now().Unix(),
			}))
			require.Error(t, err)
		}},
		{"New", func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			testID, _ := libp2ppeer.Decode("12D3KooWMn5qZpfJckxXBgRd4syQMhkkzbAFnjwPFzAJByj5vLLn")
			mockNode := &mockNode{id: testID}

			svc, err := sensors.New(mockNode, sensors.WithPort(0))
			require.NoError(t, err)
			require.NotNil(t, svc)
			require.NotNil(t, svc.Addr())

			addr := svc.Addr().(*net.TCPAddr)
			require.Equal(t, "127.0.0.1", addr.IP.String())

			time.Sleep(100 * time.Millisecond)

			_, err = svc.PushValue(ctx, connect.NewRequest(&sensorsv1.PushValueRequest{
				Name:      "test",
				Value:     123.45,
				Timestamp: time.Now().Unix(),
			}))
			require.NoError(t, err)

			cancel()
			time.Sleep(100 * time.Millisecond)
		}},
		{"New_WithRegistry", func(t *testing.T) {
			testID, _ := libp2ppeer.Decode("12D3KooWMn5qZpfJckxXBgRd4syQMhkkzbAFnjwPFzAJByj5vLLn")
			mockNode := &mockNode{id: testID}
			registry := sensors.NewRegistry()
			registry.Set("pre-existing", 99.9)

			svc, err := sensors.New(mockNode, sensors.WithPort(0), sensors.WithRegistry(registry))
			require.NoError(t, err)

			value, ok, err := svc.Registry().Get("pre-existing")
			require.NoError(t, err)
			require.True(t, ok)
			require.Equal(t, 99.9, value)
		}},
		{"Service_Path", func(t *testing.T) {
			testID, _ := libp2ppeer.Decode("12D3KooWMn5qZpfJckxXBgRd4syQMhkkzbAFnjwPFzAJByj5vLLn")
			mockNode := &mockNode{id: testID}
			service := newTestService(t, mockNode, nil)
			path := service.Path()
			require.NotEmpty(t, path)
		}},
		{"Service_Handler", func(t *testing.T) {
			testID, _ := libp2ppeer.Decode("12D3KooWMn5qZpfJckxXBgRd4syQMhkkzbAFnjwPFzAJByj5vLLn")
			mockNode := &mockNode{id: testID}
			service := newTestService(t, mockNode, nil)
			handler := service.Handler()
			require.NotNil(t, handler)
		}},
		{"Service_Addr_NilListener", func(t *testing.T) {
			t.Skip("Service created via New always has a listener")
		}},
		{"Service_PushValue_InvalidValue", func(t *testing.T) {
			testID, _ := libp2ppeer.Decode("12D3KooWMn5qZpfJckxXBgRd4syQMhkkzbAFnjwPFzAJByj5vLLn")
			mockNode := &mockNode{id: testID}
			service := newTestService(t, mockNode, nil)
			ctx := context.Background()

			_, err := service.PushValue(ctx, connect.NewRequest(&sensorsv1.PushValueRequest{
				Name:      "invalid",
				Value:     math.NaN(),
				Timestamp: time.Now().Unix(),
			}))
			require.Error(t, err)

			_, err = service.PushValue(ctx, connect.NewRequest(&sensorsv1.PushValueRequest{
				Name:      "invalid2",
				Value:     math.Inf(1),
				Timestamp: time.Now().Unix(),
			}))
			require.Error(t, err)
		}},
		{"Service_NodeInfo", func(t *testing.T) {
			testID, _ := libp2ppeer.Decode("12D3KooWMn5qZpfJckxXBgRd4syQMhkkzbAFnjwPFzAJByj5vLLn")
			mockNode := &mockNode{id: testID}
			service := newTestService(t, mockNode, nil)
			ctx := context.Background()

			resp, err := service.NodeInfo(ctx, connect.NewRequest(&sensorsv1.NodeInfoRequest{}))
			require.NoError(t, err)
			require.NotNil(t, resp)
			require.NotEmpty(t, resp.Msg.GetNodeId())
			require.Equal(t, testID.String(), resp.Msg.GetNodeId())
		}},
		{"Registry_Delete", func(t *testing.T) {
			registry := sensors.NewRegistry()

			err := registry.Set("test", 42.5)
			require.NoError(t, err)

			_, ok, err := registry.Get("test")
			require.NoError(t, err)
			require.True(t, ok)

			err = registry.Delete("test")
			require.NoError(t, err)

			_, ok, err = registry.Get("test")
			require.NoError(t, err)
			require.False(t, ok)
		}},
		{"Registry_Delete_EmptyName", func(t *testing.T) {
			registry := sensors.NewRegistry()
			err := registry.Delete("")
			require.Error(t, err)
			require.Equal(t, sensors.ErrEmptyName, err)
		}},
		{"Registry_List", func(t *testing.T) {
			registry := sensors.NewRegistry()

			registry.Set("cpu", 42.5)
			registry.Set("memory", 75.0)
			registry.Set("disk", 30.2)

			entries := registry.List()
			require.Len(t, entries, 3)

			entryMap := make(map[string]float64)
			for _, entry := range entries {
				entryMap[entry.Name] = entry.Value
			}

			require.Equal(t, 42.5, entryMap["cpu"])
			require.Equal(t, 75.0, entryMap["memory"])
			require.Equal(t, 30.2, entryMap["disk"])
		}},
		{"Registry_List_Empty", func(t *testing.T) {
			registry := sensors.NewRegistry()
			entries := registry.List()
			require.Empty(t, entries)
		}},
		{"Registry_Set_InvalidValue", func(t *testing.T) {
			registry := sensors.NewRegistry()

			err := registry.Set("test", math.NaN())
			require.Error(t, err)
			require.Equal(t, sensors.ErrInvalidValue, err)

			err = registry.Set("test", math.Inf(1))
			require.Error(t, err)
			require.Equal(t, sensors.ErrInvalidValue, err)

			err = registry.Set("test", math.Inf(-1))
			require.Error(t, err)
			require.Equal(t, sensors.ErrInvalidValue, err)
		}},
		{"Registry_Get_EmptyName", func(t *testing.T) {
			registry := sensors.NewRegistry()
			_, _, err := registry.Get("")
			require.Error(t, err)
			require.Equal(t, sensors.ErrEmptyName, err)
		}},
		{"Registry_Get_NotExists", func(t *testing.T) {
			registry := sensors.NewRegistry()
			value, ok, err := registry.Get("nonexistent")
			require.NoError(t, err)
			require.False(t, ok)
			require.Equal(t, 0.0, value)
		}},
	}
	for i, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if i > 0 {
				time.Sleep(1 * time.Second)
			}
			tc.fn(t)
		})
	}
}

func BenchmarkRegistry_Set(b *testing.B) {
	registry := sensors.NewRegistry()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		registry.Set("metric", float64(i))
	}
}

func BenchmarkRegistry_Get(b *testing.B) {
	registry := sensors.NewRegistry()
	registry.Set("metric", 42.5)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		registry.Get("metric")
	}
}

func BenchmarkRegistry_List(b *testing.B) {
	registry := sensors.NewRegistry()
	for i := 0; i < 1000; i++ {
		registry.Set(fmt.Sprintf("metric%d", i), float64(i))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		registry.List()
	}
}

func BenchmarkRegistry_Delete(b *testing.B) {
	registry := sensors.NewRegistry()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		registry.Set(fmt.Sprintf("metric%d", i), float64(i))
		registry.Delete(fmt.Sprintf("metric%d", i))
	}
}

func BenchmarkRegistry_Concurrent(b *testing.B) {
	registry := sensors.NewRegistry()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			registry.Set(fmt.Sprintf("metric%d", i), float64(i))
			registry.Get(fmt.Sprintf("metric%d", i))
			i++
		}
	})
}

func BenchmarkService_PushValue(b *testing.B) {
	testID, _ := libp2ppeer.Decode("12D3KooWMn5qZpfJckxXBgRd4syQMhkkzbAFnjwPFzAJByj5vLLn")
	mockNode := &mockNode{id: testID}

	service, _ := sensors.New(mockNode, sensors.WithPort(0))
	req := connect.NewRequest(&sensorsv1.PushValueRequest{
		Name:      "cpu",
		Value:     42.5,
		Timestamp: time.Now().Unix(),
	})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		service.PushValue(b.Context(), req)
	}
}
