package libraries

import (
	"fmt"

	"github.com/taubyte/tau/pkg/schema/basic"
	"github.com/taubyte/tau/pkg/schema/common"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

func (l *library) SetWithStruct(sync bool, library *structureSpec.Library) error {
	ops := make([]basic.Op, 0)
	var opMapper = common.Mapper{
		{"Id", false, func() error {
			ops = append(ops, Id(library.Id))
			return nil
		}},
		{"Description", false, func() error {
			ops = append(ops, Description(library.Description))
			return nil
		}},
		{"Tags", false, func() error {
			ops = append(ops, Tags(library.Tags))
			return nil
		}},
		{"Path", true, func() error {
			ops = append(ops, Path(library.Path))
			return nil
		}},
		{"Branch", true, func() error {
			ops = append(ops, Branch(library.Branch))
			return nil
		}},
		{"Provider", true, func() error {
			switch library.Provider {
			case "github":
				ops = append(ops, Github(library.RepoID, library.RepoName))
			default:
				return fmt.Errorf("Git provider `%s` not supported", library.Provider)
			}
			return nil
		}},
		{"SmartOps", true, func() error {
			ops = append(ops, SmartOps(library.SmartOps))
			return nil
		}},
	}

	err := opMapper.Run(library)
	if err != nil {
		return fmt.Errorf("mapping values failed with: %s", err)
	}

	return l.Set(sync, ops...)
}
