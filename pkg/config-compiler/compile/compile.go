package compile

import (
	"fmt"

	"github.com/taubyte/tau/pkg/config-compiler/indexer"
	projectSchema "github.com/taubyte/tau/pkg/schema/project"
	databaseSpec "github.com/taubyte/tau/pkg/specs/database"
	domainSpec "github.com/taubyte/tau/pkg/specs/domain"
	functionSpec "github.com/taubyte/tau/pkg/specs/function"
	librarySpec "github.com/taubyte/tau/pkg/specs/library"
	messagingSpec "github.com/taubyte/tau/pkg/specs/messaging"
	serviceSpec "github.com/taubyte/tau/pkg/specs/service"
	smartOpSpec "github.com/taubyte/tau/pkg/specs/smartops"
	storageSpec "github.com/taubyte/tau/pkg/specs/storage"
	websiteSpec "github.com/taubyte/tau/pkg/specs/website"
)

func (c *compiler) indexer(ctx *indexer.IndexContext, f indexerFunc) error {
	return f(ctx, c.config.Project, c.index)
}

func (c *compiler) magic(list []string, app string, f magicFunc) (map[string]interface{}, error) {
	returnMap := make(map[string]interface{}, len(list))
	for _, name := range list {
		fmt.Fprintf(c.log, "[Build|%s] Compiling %s\n", name, app)
		_id, Object, err := f(name, app, c.config.Project)
		if err != nil {
			fmt.Fprintf(c.log, "[Build|%s] failed with %s\n", name, err.Error())
			return returnMap, err
		}
		if len(Object) != 0 {
			returnMap[_id] = Object
		}
	}

	return returnMap, nil
}

func compilationGroup(project projectSchema.Project) map[string]compileObject {
	getter := project.Get()
	return map[string]compileObject{
		databaseSpec.PathVariable.String():  {Get: getter.Databases, Compile: database, Indexer: indexer.Databases},
		domainSpec.PathVariable.String():    {Get: getter.Domains, Compile: domain, Indexer: indexer.Domains},
		functionSpec.PathVariable.String():  {Get: getter.Functions, Compile: function, Indexer: indexer.Functions},
		librarySpec.PathVariable.String():   {Get: getter.Libraries, Compile: library, Indexer: indexer.Libraries},
		messagingSpec.PathVariable.String(): {Get: getter.Messaging, Compile: messaging, Indexer: indexer.Messaging},
		serviceSpec.PathVariable.String():   {Get: getter.Services, Compile: service, Indexer: nil},
		smartOpSpec.PathVariable.String():   {Get: getter.SmartOps, Compile: smartOps, Indexer: indexer.SmartOps},
		storageSpec.PathVariable.String():   {Get: getter.Storages, Compile: storage, Indexer: indexer.Storages},
		websiteSpec.PathVariable.String():   {Get: getter.Websites, Compile: website, Indexer: indexer.Websites},
	}
}
