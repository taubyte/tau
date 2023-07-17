package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	cr "bitbucket.org/taubyte/p2p/streams/command/response"
	"github.com/fxamacker/cbor/v2"
	moodyCommon "github.com/taubyte/go-interfaces/moody"
	"github.com/taubyte/go-interfaces/p2p/streams"
	iface "github.com/taubyte/go-interfaces/services/seer"
	"github.com/taubyte/utils/maps"
)

func parseUsagefromBody(body streams.Body) (iface.UsageData, error) {
	var usage iface.UsageData
	data, ok := body["usage"]
	if !ok {
		return usage, errors.New("failed getting usage from body")
	}

	// hack: marshal then unmarshal to get it into a struct
	bloc, err := cbor.Marshal(data)
	if err != nil {
		return usage, fmt.Errorf("marshalling usage data failed with %s", err)
	}

	err = cbor.Unmarshal(bloc, &usage)
	if err != nil {
		return usage, fmt.Errorf("un-marshalling usage data failed with %s", err)
	}

	return usage, nil
}

// store usage
func (srv *oracleService) heartbeatServiceHandler(ctx context.Context, conn streams.Connection, body streams.Body) (cr.Response, error) {
	action, err := maps.String(body, "action")
	if err != nil {
		action = ""
	}

	if action != "" {
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
			return srv.getInfo(id)
		}
	}

	//TODO: move this into default above
	var (
		id    string
		valid bool
	)

	if srv.seer.odo {
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

	usage, err := parseUsagefromBody(body)
	if err != nil {
		return nil, err
	}

	hostname, err := maps.String(body, "hostname")
	if err != nil {
		hostname = ""
	}

	address := conn.RemoteMultiaddr().String()
	addr := strings.Split(address, "/")

	statement, err := srv.seer.nodeDB.Prepare(UsageStatement)
	if err != nil {
		return nil, fmt.Errorf("failed heartbeat insert prepare with: %w", err)
	}

	defer statement.Close()

	srv.seer.nodeDBMutex.Lock()
	defer srv.seer.nodeDBMutex.Unlock()

	_, err = statement.Exec(
		id,
		hostname,
		time.Now().UnixNano(),
		int(usage.Memory.Used),
		int(usage.Memory.Total),
		int(usage.Memory.Free),
		int(usage.Cpu.Total),
		usage.Cpu.Count,
		usage.Cpu.User,
		usage.Cpu.Nice,
		usage.Cpu.System,
		usage.Cpu.Idle,
		usage.Cpu.Iowait,
		usage.Cpu.Irq,
		usage.Cpu.Softirq,
		usage.Cpu.Steal,
		usage.Cpu.Guest,
		usage.Cpu.GuestNice,
		usage.Cpu.StatCount,
		addr[2],
		usage.Disk.Total,
		usage.Disk.Free,
		usage.Disk.Used,
		usage.Disk.Available,
	)
	if err != nil {
		return nil, fmt.Errorf("heartbeat insert exec for hostname: `%s` failed with: %s", hostname, err)
	}

	logger.Info(moodyCommon.Object{"message": fmt.Sprintf("Inserted/Updated %s in Usage", id)})

	return cr.Response{"Updated Usage": id}, nil
}

func (srv *oracleService) listIds() (cr.Response, error) {
	var ids []string

	srv.seer.nodeDBMutex.RLock()
	row, err := srv.seer.nodeDB.Query("SELECT Id FROM Usage")
	srv.seer.nodeDBMutex.RUnlock()
	if err != nil {
		logger.Error(moodyCommon.Object{"message": fmt.Sprintf("Failed listIds query error: %v", err)})
		return nil, fmt.Errorf("failed listIds query error: %w", err)
	}
	defer row.Close()

	for row.Next() {
		var id string
		err = row.Scan(&id)
		if err != nil {
			return nil, fmt.Errorf("scanning row for id failed with: %s", err)
		}
		ids = append(ids, id)
	}

	return cr.Response{"ids": ids}, nil
}

func (srv *oracleService) listServiceIds(name string) (cr.Response, error) {
	var ids []string
	statement := fmt.Sprintf("SELECT * FROM Meta WHERE Type='%s'", name)

	srv.seer.nodeDBMutex.RLock()
	row, err := srv.seer.nodeDB.Query(statement)
	srv.seer.nodeDBMutex.RUnlock()
	if err != nil {
		logger.Error(moodyCommon.Object{"message": fmt.Sprintf("Failed listServiceIds query error: %v", err)})
		return nil, fmt.Errorf("failed listServiceIds query error: %w", err)
	}
	defer row.Close()

	for row.Next() {
		var (
			id    string
			_type string
			key   string
			value string
		)
		// TODO only using id here, need a better select `SELECT id from...`
		row.Scan(&id, &_type, &key, &value)
		ids = append(ids, id)
	}

	return cr.Response{"ids": ids}, nil
}

func (srv *oracleService) getInfo(id string) (cr.Response, error) {
	var service iface.UsageReturn
	statement := fmt.Sprintf("SELECT * FROM Usage WHERE Id=\"%s\"", id)

	srv.seer.nodeDBMutex.RLock()
	row, err := srv.seer.nodeDB.Query(statement)
	srv.seer.nodeDBMutex.RUnlock()
	if err != nil {
		logger.Error(moodyCommon.Object{"message": fmt.Sprintf("Failed query from usage for %s with: %v", id, err)})
		return nil, fmt.Errorf("failed info query error: %w", err)
	}
	defer row.Close()

	for row.Next() {
		err = row.Scan(&service.Id,
			&service.Name,
			&service.Timestamp,
			&service.UsedMem,
			&service.TotalMem,
			&service.FreeMem,
			&service.TotalCpu,
			&service.CpuCount,
			&service.CpuUser,
			&service.CpuNice,
			&service.CpuSystem,
			&service.CpuIdle,
			&service.CpuIowait,
			&service.CpuIrq,
			&service.CpuSoftirq,
			&service.CpuSteal,
			&service.CpuGuest,
			&service.CpuGuestNice,
			&service.CpuStatCount,
			&service.Address,
			&service.TotalDisk,
			&service.FreeDisk,
			&service.UsedDisk,
			&service.AvailableDisk,
		)
		if err != nil {
			return nil, fmt.Errorf("rowscan in getInfo failed with: %s", err)
		}
	}

	// Grab type from Services Table
	getType := fmt.Sprintf("SELECT Type FROM Services WHERE Id=\"%s\"", service.Id)

	srv.seer.nodeDBMutex.RLock()
	row2, err := srv.seer.nodeDB.Query(getType)
	srv.seer.nodeDBMutex.RUnlock()
	if err != nil {
		logger.Error(moodyCommon.Object{"message": fmt.Sprintf("Failed getting type from services for %s with: %v", service.Id, err)})
		return nil, fmt.Errorf("failed getting types query error: %w", err)
	}
	defer row2.Close()

	for row2.Next() {
		var stype string
		err = row2.Scan(&stype)
		if err != nil {
			return nil, fmt.Errorf("failed row2 scan error: %w", err)
		}
		service.Type = append(service.Type, stype)
	}

	serviceBytes, err := json.Marshal(service)
	if err != nil {
		logger.Error(moodyCommon.Object{"message": fmt.Sprintf("Failed marshalling service for %s with: %v", service.Id, err)})
		return nil, fmt.Errorf("marshalling service failed with: %s", err)
	}

	return map[string]interface{}{"usage": serviceBytes}, nil
}
