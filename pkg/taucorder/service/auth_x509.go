package service

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"
	pb "github.com/taubyte/tau/pkg/taucorder/proto/gen/taucorder/v1"
)

func (xs *x509Service) Delete(ctx context.Context, req *connect.Request[pb.X509CertificateRequest]) (*connect.Response[pb.Empty], error) {
	return nil, errors.New("not implemented")
}

func (xs *x509Service) Get(ctx context.Context, req *connect.Request[pb.X509CertificateRequest]) (*connect.Response[pb.X509Certificate], error) {
	ni, err := xs.getNode(req.Msg)
	if err != nil {
		return nil, err
	}

	domain := req.Msg.GetDomain()
	if domain == "" {
		return nil, errors.New("no domain provided")
	}

	acme := false
	data, err := ni.authClient.GetRawStaticCertificate(domain) // static first
	if err != nil {
		acme = true
		data, err = ni.authClient.GetRawCertificate(domain) // static first
		if err != nil {
			return nil, fmt.Errorf("fetching certificate for `%s`: %w", domain, err)
		}
	}

	return connect.NewResponse(&pb.X509Certificate{
		Data: data,
		Acme: acme,
	}), nil
}

func (xs *x509Service) List(ctx context.Context, req *connect.Request[pb.Node], stream *connect.ServerStream[pb.X509Certificate]) error {
	return errors.New("not implemented")
}

func (xs *x509Service) Set(ctx context.Context, req *connect.Request[pb.X509CertificateRequest]) (*connect.Response[pb.Empty], error) {
	ni, err := xs.getNode(req.Msg)
	if err != nil {
		return nil, err

	}

	domain := req.Msg.GetDomain()
	if domain == "" {
		return nil, errors.New("no domain provided")
	}

	data := req.Msg.GetData()
	if len(data) == 0 {
		return nil, errors.New("no certificate provided")
	}

	if err = ni.authClient.InjectStaticCertificate(domain, data); err != nil {
		return nil, fmt.Errorf("setting static certificate for `%s`: %w", domain, err)
	}

	return connect.NewResponse(&pb.Empty{}), nil
}
