package service

import (
	"context"
	"net"
	"net/http"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	pb "github.com/taubyte/tau/pkg/taucorder/proto/gen/taucorder/v1"
	pbconnect "github.com/taubyte/tau/pkg/taucorder/proto/gen/taucorder/v1/taucorderv1connect"
	_ "github.com/taubyte/tau/services/auth"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"gotest.tools/v3/assert"
)

func TestAuthX509(t *testing.T) {
	ctx, ctxC := context.WithCancel(context.Background())
	defer ctxC()

	s, err := getMockService(ctx)
	assert.NilError(t, err)

	ns := &nodeService{Service: s}
	xs := &x509Service{Service: s}

	s.addHandler(pbconnect.NewNodeServiceHandler(ns))

	uname := t.Name()
	u := dream.New(dream.UniverseConfig{
		Name: uname,
	})
	defer u.Stop()

	assert.NilError(t, u.StartWithConfig(&dream.Config{Services: map[string]common.ServiceConfig{"auth": {}}}))

	ni, err := ns.New(ctx, connect.NewRequest(&pb.Config{
		Source: &pb.Config_Universe{
			Universe: &pb.Dream{
				Universe: uname,
			},
		},
	}))
	assert.NilError(t, err)

	defer ns.Free(ctx, connect.NewRequest(ni.Msg))

	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		panic(err)
	}
	defer listener.Close()

	path, handler := pbconnect.NewX509InAuthServiceHandler(xs)

	mux := http.NewServeMux()
	mux.Handle(path, handler)

	server := &http.Server{
		Handler: h2c.NewHandler(mux, &http2.Server{}),
	}

	go func() {
		server.Serve(listener)
	}()
	defer server.Shutdown(ctx)

	injectFakeStaticCert(t, u, "1.fake.domain.com", []byte("cert-1.fake.domain.com"))
	injectFakeStaticCert(t, u, "2.fake.domain.com", []byte("cert-2.fake.domain.com"))
	injectFakeACMECert(t, u, "1.fake.acme.domain.com", []byte("cert-1.fake.acme.domain.com"))
	injectFakeACMECert(t, u, "2.fake.acme.domain.com", []byte("cert-2.fake.acme.domain.com"))

	t.Run("Get certificate", func(t *testing.T) {
		c := pbconnect.NewX509InAuthServiceClient(http.DefaultClient, "http://"+listener.Addr().String())
		for _, domain := range []string{"1.fake.domain.com", "2.fake.domain.com", "1.fake.acme.domain.com", "2.fake.acme.domain.com"} {
			cert, err := c.Get(ctx, connect.NewRequest(&pb.X509CertificateRequest{
				Node:   ni.Msg,
				Domain: domain,
			}))
			assert.NilError(t, err)

			assert.DeepEqual(t, cert.Msg.GetData(), []byte("cert-"+domain))
			assert.Equal(t, cert.Msg.GetAcme(), strings.Contains(domain, "acme"))
		}
	})

	t.Run("Set acme certificate", func(t *testing.T) {
		c := pbconnect.NewX509InAuthServiceClient(http.DefaultClient, "http://"+listener.Addr().String())
		_, err := c.Set(ctx, connect.NewRequest(&pb.X509CertificateRequest{
			Node:   ni.Msg,
			Domain: "1.fake.acme.domain.com",
			Data:   []byte("1.fake.acme.domain.com-now-static"),
		}))
		assert.NilError(t, err)

		cert, err := c.Get(ctx, connect.NewRequest(&pb.X509CertificateRequest{
			Node:   ni.Msg,
			Domain: "1.fake.acme.domain.com",
		}))
		assert.NilError(t, err)

		assert.DeepEqual(t, cert.Msg.GetData(), []byte("1.fake.acme.domain.com-now-static"))
		assert.Equal(t, cert.Msg.GetAcme(), false)
	})

	t.Run("Set static certificate", func(t *testing.T) {
		c := pbconnect.NewX509InAuthServiceClient(http.DefaultClient, "http://"+listener.Addr().String())
		_, err := c.Set(ctx, connect.NewRequest(&pb.X509CertificateRequest{
			Node:   ni.Msg,
			Domain: "1.fake.domain.com",
			Data:   []byte("1.fake.domain.com-new"),
		}))
		assert.NilError(t, err)

		cert, err := c.Get(ctx, connect.NewRequest(&pb.X509CertificateRequest{
			Node:   ni.Msg,
			Domain: "1.fake.domain.com",
		}))
		assert.NilError(t, err)

		assert.DeepEqual(t, cert.Msg.GetData(), []byte("1.fake.domain.com-new"))
		assert.Equal(t, cert.Msg.GetAcme(), false)
	})
}
