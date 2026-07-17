package compile

import (
	projectSchema "github.com/taubyte/tau/pkg/schema/project"
	"github.com/taubyte/tau/pkg/tcc/internal/parity/config-compiler/indexer"
)

type compileObject struct {
	Get     func(string) (local []string, global []string)
	Compile magicFunc
	Indexer indexerFunc
}

type indexerFunc func(
	ctx *indexer.IndexContext,
	project projectSchema.Project,
	urlIndex map[string]interface{},
) error

type magicFunc func(
	name,
	app string,
	p projectSchema.Project,
) (
	_id string,
	ReturnMap map[string]interface{},
	err error,
)
