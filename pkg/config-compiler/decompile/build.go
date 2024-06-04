package decompile

import (
	"fmt"
	"reflect"

	"github.com/spf13/afero"
	projectLib "github.com/taubyte/tau/pkg/schema/project"
)

type Option func(d *decompiler) error

type decompiler struct {
	project projectLib.Project
	object  interface{}
}

func New(fs afero.Fs, obj interface{}, options ...Option) (d *decompiler, err error) {
	d = &decompiler{object: obj}
	d.project, err = projectLib.Open(projectLib.VirtualFS(fs, "/"))
	if err != nil {
		return
	}

	for _, opt := range options {
		err = opt(d)
		if err != nil {
			return
		}
	}

	return
}

func (d *decompiler) Build() (projectLib.Project, error) {
	// Get all resources
	rValue := reflect.ValueOf(d.object)
	if rValue.Kind() != reflect.Map {
		return nil, fmt.Errorf("object is not a map")
	}
	for _, key := range rValue.MapKeys() {
		rData := rValue.MapIndex(key).Elem()
		if key.Kind() == reflect.Interface {
			key = key.Elem()
		}
		var err error
		switch key.String() {
		case "id":
			err = d.project.Set(false, projectLib.Id(rData.String()))
		case "name":
			err = d.project.Set(false, projectLib.Name(rData.String()))
		case "description":
			err = d.project.Set(false, projectLib.Description(rData.String()))
		case "email":
			err = d.project.Set(false, projectLib.Email(rData.String()))
		case "applications":
			if rData.Kind() != reflect.Map {
				return nil, fmt.Errorf("application object is `%s`, not a map: %#v", rData.Kind(), rData)
			}
			for _, _key := range rData.MapKeys() {
				_rData := rData.MapIndex(_key).Elem()

				if _key.Kind() == reflect.Interface {
					_key = _key.Elem()
				}
				err = d.application(_key.String(), _rData.Interface())
				if err != nil {
					break
				}
			}
		default:
			err = d.resource(key.String(), rData.Interface(), "")
		}
		if err != nil {
			return nil, err
		}
	}

	err := d.cleanResources()
	if err != nil {
		return nil, fmt.Errorf("cleaning failed with: %v", err)
	}

	// Sync
	err = d.project.Set(true)
	if err != nil {
		return nil, fmt.Errorf("sync failed with: %v", err)
	}

	return d.project, nil
}
