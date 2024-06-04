package databases

import (
	"github.com/taubyte/tau/pkg/schema/common"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

func (g getter) Struct() (db *structureSpec.Database, err error) {
	key, _ /*keyType*/ := g.Encryption()

	size, err := common.StringToUnits(g.Size())
	if err != nil {
		return nil, err
	}
	db = &structureSpec.Database{
		Id:          g.Id(),
		Name:        g.Name(),
		Description: g.Description(),
		Tags:        g.Tags(),
		Match:       g.Match(),
		Regex:       g.Regex(),
		Local:       g.Local(),
		Key:         key,
		Min:         g.Min(),
		Max:         g.Max(),
		Size:        uint64(size),
	}

	return
}
