package decompile

import (
	"fmt"
	"reflect"

	projectLib "github.com/taubyte/tau/pkg/schema/project"
	databaseSpec "github.com/taubyte/tau/pkg/specs/database"
	domainSpec "github.com/taubyte/tau/pkg/specs/domain"
	functionSpec "github.com/taubyte/tau/pkg/specs/function"
	librarySpec "github.com/taubyte/tau/pkg/specs/library"
	messagingSpec "github.com/taubyte/tau/pkg/specs/messaging"
	serviceSpec "github.com/taubyte/tau/pkg/specs/service"
	smartopsSpec "github.com/taubyte/tau/pkg/specs/smartops"
	storagesSpec "github.com/taubyte/tau/pkg/specs/storage"
	websitesSpec "github.com/taubyte/tau/pkg/specs/website"
)

type magicFunc func(project projectLib.Project, _id string, obj interface{}, appName string) error

func magic(f magicFunc, project projectLib.Project, obj interface{}, appName string) (err error) {

	rValue := reflect.ValueOf(obj)
	if rValue.Kind() != reflect.Map {
		return fmt.Errorf("object is not a map:  `%s`(%T), %#v", rValue.Type().Name(), obj, rValue)
	}

	for _, key := range rValue.MapKeys() {
		rData := rValue.MapIndex(key).Elem()
		if key.Kind() == reflect.Interface {
			key = key.Elem()
		}
		err = f(project, key.String(), rData.Interface(), appName)
		if err != nil {
			return err
		}
	}

	return nil
}

func (d *decompiler) resource(key string, data interface{}, appName string) error {
	_router := map[string]magicFunc{
		databaseSpec.PathVariable.String():  database,
		domainSpec.PathVariable.String():    domain,
		functionSpec.PathVariable.String():  function,
		librarySpec.PathVariable.String():   library,
		messagingSpec.PathVariable.String(): messaging,
		serviceSpec.PathVariable.String():   service,
		smartopsSpec.PathVariable.String():  smartop,
		storagesSpec.PathVariable.String():  storage,
		websitesSpec.PathVariable.String():  website,
	}

	handler, exist := _router[key]
	if !exist {
		return fmt.Errorf("resource `%s` doesn't exist", key)
	}

	return magic(handler, d.project, data, appName)
}
