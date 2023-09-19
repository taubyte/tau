package seer

import (
	"database/sql"
	"fmt"
	"os"
	"path"
	"time"

	iface "github.com/taubyte/go-interfaces/services/seer"
	"github.com/taubyte/tau/config"
)

func (srv *oracleService) insertService(id string, service iface.ServiceInfo) error {
	statement, err := srv.seer.nodeDB.Prepare(InsertService)
	if err != nil {
		return fmt.Errorf("insertService prepare failed with: %s", err)
	}

	defer statement.Close()

	srv.seer.nodeDBMutex.Lock()
	defer srv.seer.nodeDBMutex.Unlock()
	_, err = statement.Exec(id, time.Now().UnixNano(), service.Type)
	if err != nil {
		return fmt.Errorf("insertService exec failed with: %s", err)
	}

	return nil
}

func (srv *oracleService) insertMeta(id string, mtype iface.ServiceType, key string, value string) error {
	statement, err := srv.seer.nodeDB.Prepare(InsertMeta)
	if err != nil {
		return fmt.Errorf("meta prepare failed with: %s", err)
	}

	defer statement.Close()

	srv.seer.nodeDBMutex.Lock()
	defer srv.seer.nodeDBMutex.Unlock()
	_, err = statement.Exec(id, mtype, key, value)
	if err != nil {
		return fmt.Errorf("meta exec exec failed with: %s", err)
	}

	return nil
}

// TODO: Make this function and one below into one
func (h *dnsHandler) getServiceIp(service string) ([]string, error) {
	var ips []string

	unique := make(map[string]bool, 0)

	h.seer.nodeDBMutex.RLock()
	rows, err := h.seer.nodeDB.Query(GetServiceIp, time.Now().UnixNano()-ValidServiceResponseTime.Nanoseconds(), service)
	h.seer.nodeDBMutex.RUnlock()
	if err != nil {
		return nil, fmt.Errorf("getServiceIp for `%s` query failed with: %s", service, err)
	}
	defer rows.Close()

	for rows.Next() {
		var ip string
		if err := rows.Scan(&ip); err != nil {
			return nil, fmt.Errorf("getServiceIp for `%s` scan failed with: %s", service, err)
		}
		if _, ok := unique[ip]; !ok {
			unique[ip] = true
			ips = append(ips, ip)
		}
	}

	return ips, nil

}

func (h *dnsHandler) getNodeIp() ([]string, error) {
	var ips []string

	// Check cache for node ips
	nodeIps := h.cache.Get("gateway")
	if nodeIps != nil {
		return nodeIps.Value(), nil
	}

	unique := make(map[string]bool, 0)
	// Query for nodes that have responded in the last 5 minutes
	h.seer.nodeDBMutex.RLock()
	rows, err := h.seer.nodeDB.Query(GetStableNodeIps, time.Now().UnixNano()-ValidServiceResponseTime.Nanoseconds())
	h.seer.nodeDBMutex.RUnlock()
	if err != nil {
		return nil, fmt.Errorf("getNodeIp query failed with: %s", err)
	}
	defer rows.Close()

	for rows.Next() {
		var ip string
		if err := rows.Scan(&ip); err != nil {
			return nil, fmt.Errorf("getNodeIp scan failed with: %s", err)
		}
		if _, ok := unique[ip]; !ok {
			unique[ip] = true
			ips = append(ips, ip)
		}
	}

	// Cache substrate ips
	h.cache.Set("gateway", ips, 5*time.Minute)

	return ips, nil
}

func initializeDB(srv *Service, config *config.Node) error {
	var file *os.File
	var err error
	dbPath := path.Join(config.Root, "storage", srv.shape, NodeDatabaseFileName)
	// Create SQLite DB
	file, err = os.Open(dbPath)
	if err != nil {
		// If file doesnt exist create the file
		_, err := os.Stat(dbPath)
		if err != nil {
			file, err = os.Create(dbPath)
			if err != nil {
				return fmt.Errorf("creating file after stat failed with: %s", err)
			}
		} else {
			//If file does exist rename and create new database
			newFileName := fmt.Sprintf("%s%d", file.Name(), time.Now().UnixNano())

			if err := os.Rename(file.Name(), newFileName); err != nil {
				return fmt.Errorf("renaming file `%s` to `%s` failed with: %s", file.Name(), newFileName, err)
			}

			file, err = os.Create(dbPath)
			if err != nil {
				return fmt.Errorf("creating file at `%s` with: %s", dbPath, err)
			}
		}
	}

	fileName := file.Name()
	err = file.Close()
	if err != nil {
		return fmt.Errorf("closing file `%s` failed with: %s", fileName, err)
	}

	srv.nodeDB, err = sql.Open("sqlite", fileName)
	if err != nil {
		return fmt.Errorf("opening sql file `%s` failed with: %s", fileName, err)
	}

	// TODO: Read file and execute line by line
	// Create tables for database
	err = initializeTables(srv.nodeDB)
	if err != nil {
		logger.Error("initializing table failed with:", err.Error())
		return fmt.Errorf("initializing table failed with: %s", err)
	}

	return nil
}

func (srv *oracleService) insertHandler(id string, services iface.Services) ([]string, error) {
	var err error
	var registered []string

	logger.Infof("Inserting services: %s, for id: %s", services, id)
	for _, service := range services {
		err = srv.insertService(id, service)
		if err != nil {
			return nil, err
		}
		registered = append(registered, string(service.Type))
		if service.Meta != nil {
			for key, value := range service.Meta {
				err = srv.insertMeta(id, service.Type, key, value)
				if err != nil {
					return nil, err
				}
			}
		} else {
			err = srv.insertMeta(id, service.Type, "", "")
			if err != nil {
				return nil, err
			}
		}
	}

	return registered, nil
}
