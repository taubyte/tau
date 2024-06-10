package api

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"

	"github.com/taubyte/tau/p2p/streams"
	"github.com/taubyte/tau/p2p/streams/command"
	cr "github.com/taubyte/tau/p2p/streams/command/response"
	"github.com/taubyte/utils/maps"
)

func (s *StreamHandler) getHandler(ctx context.Context, conn streams.Connection, body command.Body) (cr.Response, error) {
	projectID, err := maps.String(body, "projectID")
	if err != nil {
		return nil, err
	}

	key, err := maps.String(body, "key")
	if err != nil {
		return nil, err
	}

	db, err := s.srv.Global(projectID)
	if err != nil {
		return nil, err
	}

	byteValue, err := db.KV().Get(ctx, key)
	if err != nil {
		return nil, err
	}

	_type, err := maps.String(body, "type")
	if err != nil {
		return nil, err
	}

	value, err := convertToValue(_type, byteValue)
	if err != nil {
		return nil, err
	}

	return cr.Response{"value": value}, nil
}

func convertToValue(_type string, value []byte) (interface{}, error) {
	switch _type {
	case "uint32":
		// TODO, this will panic rather than error....
		return binary.BigEndian.Uint32(value), nil
	case "uint64":
		// TODO, this will panic rather than error....
		return binary.BigEndian.Uint64(value), nil
	case "float32":
		bits := binary.BigEndian.Uint32(value)
		return math.Float32frombits(bits), nil
	case "float64":
		bits := binary.BigEndian.Uint64(value)
		return math.Float64frombits(bits), nil
	case "string":
		return string(value), nil
	default:
		return "", fmt.Errorf("invalid type %s", _type)
	}
}
