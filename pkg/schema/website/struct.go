package website

import (
	"fmt"

	"github.com/taubyte/tau/pkg/schema/basic"
	"github.com/taubyte/tau/pkg/schema/common"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

func (w *website) SetWithStruct(sync bool, website *structureSpec.Website) error {
	ops := make([]basic.Op, 0)
	var opMapper = common.Mapper{
		{"Id", false, func() error {
			ops = append(ops, Id(website.Id))
			return nil
		}},
		{"Description", false, func() error {
			ops = append(ops, Description(website.Description))
			return nil
		}},
		{"Tags", false, func() error {
			ops = append(ops, Tags(website.Tags))
			return nil
		}},
		{"Domains", false, func() error {
			ops = append(ops, Domains(website.Domains))
			return nil
		}},
		{"Paths", true, func() error {
			ops = append(ops, Paths(website.Paths))
			return nil
		}},
		{"Branch", true, func() error {
			ops = append(ops, Branch(website.Branch))
			return nil
		}},
		{"Provider", true, func() error {
			switch website.Provider {
			case "github":
				ops = append(ops, Github(website.RepoID, website.RepoName))
			default:
				return fmt.Errorf("Git provider `%s` not supported", website.Provider)
			}
			return nil
		}},
		{"SmartOps", true, func() error {
			ops = append(ops, SmartOps(website.SmartOps))
			return nil
		}},
	}

	err := opMapper.Run(website)
	if err != nil {
		return fmt.Errorf("mapping values failed with: %s", err)
	}

	return w.Set(sync, ops...)
}
