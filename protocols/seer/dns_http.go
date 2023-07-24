package seer

import (
	"fmt"

	iface "github.com/taubyte/go-interfaces/services/seer"
	http "github.com/taubyte/http"
)

func (srv *Service) setupDnsHTTPRoutes() {
	host := ""
	if len(srv.hostUrl) > 0 {
		host = "seer.tau." + srv.hostUrl
	}

	srv.http.GET(&http.RouteDefinition{
		Host: host,
		Path: "/services/all",
		Vars: http.Variables{
			Required: []string{},
		},
		Scope:   []string{"services/query/all"},
		Handler: srv.getDnsAllServices,
	})

	srv.http.GET(&http.RouteDefinition{
		Host: host,
		Path: "/services/{id}",
		Vars: http.Variables{
			Required: []string{"id"},
		},
		Scope:   []string{"services/query/{id}"},
		Handler: srv.getDnsService,
	})

	srv.http.GET(&http.RouteDefinition{
		Host:    host,
		Path:    "/network/config",
		Scope:   []string{"network/config"},
		Handler: srv.getGeneratedDomain,
	})
}

func (srv *Service) getDnsAllServices(ctx http.Context) (interface{}, error) {
	var services []iface.UsageReturn

	srv.nodeDBMutex.RLock()
	row, err := srv.nodeDB.Query("SELECT * FROM Usage")
	srv.nodeDBMutex.RUnlock()
	if err != nil {
		return nil, fmt.Errorf("getDnsAllServices query failed with: %s", err)
	}
	defer row.Close()

	for row.Next() {
		var service iface.UsageReturn
		err = row.Scan(&service.Id, &service.Name, &service.Timestamp, &service.UsedMem, &service.TotalMem, &service.FreeMem, &service.TotalCpu, &service.CpuCount, &service.CpuUser, &service.CpuNice, &service.CpuSystem, &service.CpuIdle, &service.CpuIowait, &service.CpuIrq, &service.CpuSoftirq, &service.CpuSteal, &service.CpuGuest, &service.CpuGuestNice, &service.CpuStatCount, &service.Address)
		if err != nil {
			return nil, fmt.Errorf("rowscan in getDnsAllServices failed with: %s", err)
		}

		// Grab type from Services Table
		getService := fmt.Sprintf("SELECT Type FROM Services WHERE Id=\"%s\"", service.Id)

		srv.nodeDBMutex.RLock()
		row2, err := srv.nodeDB.Query(getService)
		srv.nodeDBMutex.RUnlock()
		if row2 != nil {
			defer row2.Close()
		}
		if err != nil {
			return nil, fmt.Errorf("failed getting types query error: %s", err)
		}

		for row2.Next() {
			var stype string
			err = row2.Scan(&stype)
			if err != nil {
				return nil, fmt.Errorf("failed row2 scan error: %s", err)
			}
			service.Type = append(service.Type, stype)
		}
		services = append(services, service)
	}

	return services, nil
}

func (srv *Service) getDnsService(ctx http.Context) (interface{}, error) {
	var service iface.UsageReturn

	id, err := ctx.GetStringVariable("id")
	if err != nil {
		return nil, fmt.Errorf("getting id from body failed with: %s", err)
	}
	getService := fmt.Sprintf("SELECT * FROM Usage WHERE Id=\"%s\"", id)

	srv.nodeDBMutex.RLock()
	row, err := srv.nodeDB.Query(getService)
	srv.nodeDBMutex.RUnlock()
	if err != nil {
		logger.Errorf("getting service %s from usage failed with: %w", id, err)
		return nil, fmt.Errorf("getHttpServices query failed with: %s", err)
	}
	defer row.Close()

	for row.Next() {
		err = row.Scan(&service.Id, &service.Name, &service.Timestamp, &service.UsedMem, &service.TotalMem, &service.FreeMem, &service.TotalCpu, &service.CpuCount, &service.CpuUser, &service.CpuNice, &service.CpuSystem, &service.CpuIdle, &service.CpuIowait, &service.CpuIrq, &service.CpuSoftirq, &service.CpuSteal, &service.CpuGuest, &service.CpuGuestNice, &service.CpuStatCount, &service.Address)
		if err != nil {
			return nil, fmt.Errorf("rowScan failed with: %s", err)
		}
	}

	return service, nil
}

func (srv *Service) getGeneratedDomain(ctx http.Context) (interface{}, error) {
	return srv.generatedDomain, nil
}
