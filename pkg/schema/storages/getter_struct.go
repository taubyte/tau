package storages

import (
	"fmt"

	"github.com/taubyte/tau/pkg/schema/common"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

func (g getter) Struct() (stg *structureSpec.Storage, err error) {
	size, err := common.StringToUnits(g.Size())
	if err != nil {
		return nil, err
	}

	_type := g.Type()
	stg = &structureSpec.Storage{
		Id:          g.Id(),
		Name:        g.Name(),
		Description: g.Description(),
		Tags:        g.Tags(),
		Match:       g.Match(),
		Regex:       g.Regex(),
		Type:        _type,
		Public:      g.Public(),
		Size:        uint64(size),
	}

	switch _type {
	case "object":
		stg.Versioning = g.Versioning()
	case "streaming":
		stg.Ttl, err = common.StringToTime(g.TTL())
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unknown storage type: %s", _type)
	}

	return
}
