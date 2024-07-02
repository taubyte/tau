package seer

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/ipfs/go-datastore"
	"github.com/ipfs/go-datastore/query"
	iface "github.com/taubyte/tau/core/services/seer"
	"github.com/taubyte/tau/p2p/streams"
	"github.com/taubyte/tau/p2p/streams/command"
	cr "github.com/taubyte/tau/p2p/streams/command/response"
	servicesCommon "github.com/taubyte/tau/services/common"
	"github.com/taubyte/utils/maps"
)

func int64ToBytes(i int64) []byte {
	bs := make([]byte, 8)
	binary.BigEndian.PutUint64(bs, uint64(i)) // BigEndian just like TCP/IP
	return bs
}

func bytesToInt64(data []byte) int64 {
	if data == nil || len(data) != 8 {
		return 0
	}

	return int64(binary.BigEndian.Uint64(data))
}

func (srv *oracleService) insertUsage(ctx context.Context, id, hostname, ip string, usage *iface.UsageData) error {
	usageData, err := cbor.Marshal(usage)
	if err != nil {
		return fmt.Errorf("marshalling usage data failed with %s", err)
	}

	b, err := srv.ds.Batch(ctx)
	if err != nil {
		return fmt.Errorf("failed to update usage with %w", err)
	}

	b.Put(ctx, datastore.NewKey("/hb/ts").Instance(id), int64ToBytes(time.Now().UnixNano()))
	b.Put(ctx, datastore.NewKey("/hb/usage").Instance(id), usageData)
	if hostname != "" {
		b.Put(ctx, datastore.NewKey("/hostname/id").Instance(id), []byte(hostname))
	}

	if ip != "" {
		b.Put(ctx, datastore.NewKey("/ip").Instance(id), []byte(ip))
	}

	err = b.Commit(ctx)
	if err != nil {
		return fmt.Errorf("failed to commit usage update with %w", err)
	}

	return nil
}

// store usage
func (srv *oracleService) heartbeatServiceHandler(ctx context.Context, conn streams.Connection, body command.Body) (cr.Response, error) {
	action, err := maps.String(body, "action")
	if err != nil {
		action = ""
	}

	switch action {
	case "list":
		return srv.listIds()
	case "listService":
		name, err := maps.String(body, "name")
		if err != nil {
			return nil, err
		}

		if name == "" {
			return nil, errors.New(("name cannot be empty"))
		}

		return srv.listServiceIds(name)
	case "info":
		id, err := maps.String(body, "id")
		if err != nil || id == "" {
			return nil, fmt.Errorf("id cannot be empty")
		}
		return srv.getInfo(ctx, id)
	default:

	}

	//TODO: move this into default above
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

	usageData, err := maps.ByteArray(body, "usage")
	if err != nil {
		return nil, fmt.Errorf("getting usage from body failed with %w", err)
	}

	var usage iface.UsageData
	err = cbor.Unmarshal(usageData, &usage)
	if err != nil {
		return nil, fmt.Errorf("un-marshalling usage data failed with %s", err)
	}

	hostname, err := maps.String(body, "hostname")
	if err != nil {
		hostname = ""
	}

	address := conn.RemoteMultiaddr().String()
	addr := strings.Split(address, "/")
	if len(addr) < 3 {
		return nil, errors.New("malformed address")
	}
	ip := addr[2]

	err = srv.insertUsage(ctx, id, hostname, ip, &usage)
	if err != nil {
		return nil, err
	}

	// Send ip's of services to all seer to store
	nodeData := &nodeData{
		Cid:   id,
		Usage: &usage,
	}

	nodeBytes, err := cbor.Marshal(nodeData)
	if err != nil {
		return nil, fmt.Errorf("failed marshalling node %s with %v", id, err)
	}

	err = srv.node.PubSubPublish(ctx, servicesCommon.OraclePubSubPath, nodeBytes)
	if err != nil {
		return nil, fmt.Errorf("sending node `%s` from seer `%s` over pubsub failed with: %s", id, srv.node.ID(), err)
	}

	return cr.Response{"Updated Usage": id}, nil
}

func (srv *oracleService) listIds() (cr.Response, error) {
	result, err := srv.ds.Query(
		srv.node.Context(),
		query.Query{Prefix: "/hostname", KeysOnly: true},
	)
	if err != nil {
		return nil, err
	}

	ids := make([]string, 0)
	for entry := range result.Next() {
		ids = append(ids, datastore.NewKey(entry.Key).Name())
	}

	return cr.Response{"ids": ids}, nil
}

func (srv *oracleService) listServiceIds(name string) (cr.Response, error) {
	result, err := srv.ds.Query(
		srv.node.Context(),
		query.Query{
			Prefix:   datastore.NewKey("/proto").String(),
			KeysOnly: true,
		},
	)
	if err != nil {
		return nil, err
	}

	ids := make([]string, 0)
	for entry := range result.Next() {
		if datastore.NewKey(entry.Key).Type() == name {
			ids = append(ids, datastore.NewKey(entry.Key).Name())
		}
	}

	return cr.Response{"ids": ids}, nil
}

func (srv *oracleService) getInfo(ctx context.Context, id string) (cr.Response, error) {
	usageData, err := srv.ds.Get(ctx, datastore.NewKey("/hb/usage").Instance(id))
	if err != nil {
		return nil, err
	}

	var usage iface.UsageData
	err = cbor.Unmarshal(usageData, &usage)
	if err != nil {
		return nil, fmt.Errorf("un-marshalling usage data failed with %s", err)
	}

	tsBytes, err := srv.ds.Get(ctx, datastore.NewKey("/hb/ts").Instance(id))
	if err != nil {
		return nil, err
	}

	ts := bytesToInt64(tsBytes)

	hnameBytes, err := srv.ds.Get(ctx, datastore.NewKey("/hostname/id").Instance(id))
	if err != nil {
		return nil, err
	}

	hostname := string(hnameBytes)

	protosResult, err := srv.ds.Query(
		ctx,
		query.Query{
			Prefix:   datastore.NewKey("/node/proto").ChildString(id).String(),
			KeysOnly: true,
		},
	)
	if err != nil {
		return nil, err
	}

	types := make([]string, 0)
	for entry := range protosResult.Next() {
		types = append(types, datastore.NewKey(entry.Key).Name())
	}

	ipBytes, err := srv.ds.Get(ctx, datastore.NewKey("/ip").Instance(id))
	if err != nil {
		return nil, err
	}

	service := iface.UsageReturn{
		Id:            id,
		Name:          hostname,
		Type:          types,
		Timestamp:     int(ts),
		UsedMem:       int(usage.Memory.Used),
		TotalMem:      int(usage.Memory.Total),
		FreeMem:       int(usage.Memory.Free),
		TotalCpu:      int(usage.Cpu.Total),
		CpuCount:      int(usage.Cpu.Count),
		CpuUser:       int(usage.Cpu.User),
		CpuNice:       int(usage.Cpu.Nice),
		CpuSystem:     int(usage.Cpu.System),
		CpuIdle:       int(usage.Cpu.Idle),
		CpuIowait:     int(usage.Cpu.Iowait),
		CpuIrq:        int(usage.Cpu.Irq),
		CpuSoftirq:    int(usage.Cpu.Softirq),
		CpuSteal:      int(usage.Cpu.Steal),
		CpuGuest:      int(usage.Cpu.Guest),
		CpuGuestNice:  int(usage.Cpu.Guest),
		CpuStatCount:  int(usage.Cpu.StatCount),
		Address:       string(ipBytes),
		TotalDisk:     int(usage.Disk.Total),
		FreeDisk:      int(usage.Disk.Free),
		UsedDisk:      int(usage.Disk.Used),
		AvailableDisk: int(usage.Disk.Available),
	}

	serviceBytes, err := json.Marshal(service)
	if err != nil {
		logger.Errorf("marshalling service for %s failed with: %s", service.Id, err.Error())
		return nil, fmt.Errorf("marshalling service failed with: %s", err)
	}

	return map[string]interface{}{"usage": serviceBytes}, nil
}
