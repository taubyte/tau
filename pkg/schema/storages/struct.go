package storages

import (
	"fmt"

	"github.com/taubyte/tau/pkg/schema/basic"
	"github.com/taubyte/tau/pkg/schema/common"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

func (s *storage) SetWithStruct(sync bool, storage *structureSpec.Storage) error {
	ops := make([]basic.Op, 0)
	var opMapper = common.Mapper{
		{"Id", false, func() error {
			ops = append(ops, Id(storage.Id))
			return nil
		}},
		{"Description", false, func() error {
			ops = append(ops, Description(storage.Description))
			return nil
		}},
		{"Tags", false, func() error {
			ops = append(ops, Tags(storage.Tags))
			return nil
		}},
		{"Match", false, func() error {
			ops = append(ops, Match(storage.Match))
			return nil
		}},
		{"Regex", false, func() error {
			ops = append(ops, Regex(storage.Regex))
			return nil
		}},
		{"Public", false, func() error {
			ops = append(ops, Public(storage.Public))
			return nil
		}},
		{"Type", false, func() error {
			_size := common.UnitsToString(storage.Size)
			switch storage.Type {
			case "object":
				ops = append(ops, Object(storage.Versioning, _size))
			case "streaming":
				ops = append(ops, Streaming(common.TimeToString(storage.Ttl), _size))
			default:
				return fmt.Errorf("Storage type `%s` not allowed", storage.Type)
			}
			return nil
		}},
		{"SmartOps", true, func() error {
			ops = append(ops, SmartOps(storage.SmartOps))
			return nil
		}},
	}

	err := opMapper.Run(storage)
	if err != nil {
		return fmt.Errorf("mapping values failed with: %s", err)
	}

	return s.Set(sync, ops...)
}
