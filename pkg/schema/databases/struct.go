package databases

import (
	"fmt"

	"github.com/taubyte/tau/pkg/schema/basic"
	"github.com/taubyte/tau/pkg/schema/common"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

func (d *database) SetWithStruct(sync bool, db *structureSpec.Database) error {
	ops := make([]basic.Op, 0)
	var opMapper = common.Mapper{
		{"Id", false, func() error {
			ops = append(ops, Id(db.Id))
			return nil
		}},
		{"Description", true, func() error {
			ops = append(ops, Description(db.Description))
			return nil
		}},
		{"Tags", true, func() error {
			ops = append(ops, Tags(db.Tags))
			return nil
		}},
		{"Match", false, func() error {
			ops = append(ops, Match(db.Match))
			return nil
		}},
		{"Regex", false, func() error {
			ops = append(ops, Regex(db.Regex))
			return nil
		}},
		{"Local", false, func() error {
			ops = append(ops, Local(db.Local))
			return nil
		}},
		{"Size", true, func() error {
			ops = append(ops, Storage(common.UnitsToString(db.Size)))
			return nil
		}},
		{"Key", true, func() error {
			ops = append(ops, Encryption(db.Key))
			return nil
		}},
		{"SmartOps", true, func() error {
			ops = append(ops, SmartOps(db.SmartOps))
			return nil
		}},
	}

	err := opMapper.Run(db)
	if err != nil {
		return fmt.Errorf("appending values failed with: %s", err)
	}

	ops = append(ops, Replicas(db.Min, db.Max))

	return d.Set(sync, ops...)
}
