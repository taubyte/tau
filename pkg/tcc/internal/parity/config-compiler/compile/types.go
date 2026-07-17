package compile

import (
	"github.com/taubyte/tau/pkg/tcc/internal/parity/config-compiler/indexer"
	projectSchema "github.com/taubyte/tau/pkg/tcc/internal/parity/schema/project"
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
