package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	cr "bitbucket.org/taubyte/p2p/streams/command/response"
	"github.com/fxamacker/cbor/v2"
	moodyCommon "github.com/taubyte/go-interfaces/moody"
	"github.com/taubyte/go-interfaces/p2p/streams"
	"github.com/taubyte/odo/protocols/billing/common"
	"github.com/taubyte/utils/maps"
)

func (srv *BillingService) countersHandler(ctx context.Context, conn streams.Connection, body streams.Body) (cr.Response, error) {
	action, err := maps.String(body, "action")
	if err != nil {
		return nil, fmt.Errorf("Failed finding action in body with %v", err)
	}

	report, err := parseReportFromBody(body)
	if err != nil {
		return nil, fmt.Errorf("Failed parsing report from body with %v", err)
	}

	switch action {
	case "stash":
		srv.stash(ctx, report)
		return nil, nil
	default:
		return nil, fmt.Errorf("Action %s not found under counters stream handler", action)
	}
}

func (srv *BillingService) stash(ctx context.Context, counters []counter) {
	for _, counter := range counters {
		err := srv.db.Put(ctx, counter.key, []byte(fmt.Sprintf("%v", counter.value)))
		if err != nil {
			logger.Error(moodyCommon.Object{"message": fmt.Sprintf("Failed stashing counter %s in database with %v", counter.key, err)})
		}
	}
}

func parseReportFromBody(body streams.Body) ([]counter, error) {
	var report map[string]interface{}
	data, ok := body["data"]
	if ok == false {
		return nil, errors.New("Data was not found in body")
	}

	bloc, err := cbor.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("Marshalling data failed with %s", err.Error())
	}

	err = cbor.Unmarshal(bloc, &report)
	if err != nil {
		return nil, fmt.Errorf("Unmarshalling data failed with %s", err.Error())
	}

	var counters []counter
	for key, _metric := range report {
		path := strings.Join([]string{common.CountersPrefix, fmt.Sprint(time.Now().Year()), fmt.Sprint(int(time.Now().Month())), key}, "/")
		temp := counter{key: path}

		value, err := maps.InterfaceToStringKeys(_metric)
		if err != nil {
			return nil, fmt.Errorf("Failed InterfaceToStringKeys with %v", err)
		}

		temp.value, ok = value["Value"]
		if !ok {
			return nil, fmt.Errorf("Did not find a value for metric %s", key)
		}

		counters = append(counters, temp)
	}

	return counters, nil
}
