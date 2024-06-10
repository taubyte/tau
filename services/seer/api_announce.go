package seer

import (
	"context"
	"fmt"

	"github.com/fxamacker/cbor/v2"
	"github.com/taubyte/tau/core/services/seer"
	"github.com/taubyte/tau/p2p/streams"
	"github.com/taubyte/tau/p2p/streams/command"
	cr "github.com/taubyte/tau/p2p/streams/command/response"
	servicesCommon "github.com/taubyte/tau/services/common"
)

func parseServicefromBody(body command.Body) (seer.Services, error) {
	var services seer.Services
	data, ok := body["services"]
	if !ok {
		return services, fmt.Errorf("failed getting service from body")
	}

	// hack: marshal then unmarshal to get it into a struct
	bloc, err := cbor.Marshal(data)
	if err != nil {
		return services, fmt.Errorf("marshalling data failed with %s", err.Error())
	}

	err = cbor.Unmarshal(bloc, &services)
	if err != nil {
		return services, fmt.Errorf("unmarshalling data failed with %s", err.Error())
	}

	return services, nil
}

// store service
func (srv *oracleService) announceServiceHandler(ctx context.Context, conn streams.Connection, body command.Body) (cr.Response, error) {
	allServices, err := parseServicefromBody(body)
	if err != nil {
		return nil, err
	}

	var (
		id    string
		valid bool
	)

	if srv.odo {
		id, valid, err = validateSignature(body)
		if err != nil {
			return nil, err
		}

		if !valid {
			return nil, fmt.Errorf("signature was not valid")
		}
	} else {
		id = conn.RemotePeer().String()
	}

	registered, err := srv.insertHandler(ctx, id, allServices)
	if err != nil {
		return nil, err
	}

	// Send ip's of services to all seer to store
	nodeData := &nodeData{
		Cid:      id,
		Services: &allServices,
	}

	nodeBytes, err := cbor.Marshal(nodeData)
	if err != nil {
		return nil, fmt.Errorf("failed marshalling node %s with %v", id, err)
	}

	err = srv.node.PubSubPublish(ctx, servicesCommon.OraclePubSubPath, nodeBytes)
	if err != nil {
		return nil, fmt.Errorf("sending node `%s` from seer `%s` over pubsub failed with: %s", id, srv.node.ID(), err)
	}

	return cr.Response{"Registered": registered}, nil
}
