package decompile

import (
	"fmt"
	"reflect"

	"github.com/mitchellh/mapstructure"
	appLib "github.com/taubyte/tau/pkg/schema/application"
)

type AppStruct struct {
	Name        string `mapstructure:"name"`
	Description string
	Tags        []string
}

func (d *decompiler) application(_id string, obj interface{}) error {
	var appO AppStruct
	mapstructure.Decode(obj, &appO)

	app, _ := d.project.Application(appO.Name)
	app.Set(false,
		appLib.Id(_id),
		appLib.Description(appO.Description),
		appLib.Tags(appO.Tags),
	)
	var err error

	rValue := reflect.ValueOf(obj)
	if rValue.Kind() != reflect.Map {
		return fmt.Errorf("application Object `%s`, not a map: %#v", rValue.Kind(), rValue)
	}

	for _, key := range rValue.MapKeys() {
		data := rValue.MapIndex(key).Elem()
		if key.Kind() == reflect.Interface {
			key = key.Elem()
		}
		switch key.String() {
		case "id", "name", "description", "tags":
		default:
			if data.Kind() != reflect.Map {
				return fmt.Errorf("resource object is `%s`, not a map: %#v", data.Kind(), data)
			}
			err = d.resource(key.String(), data.Interface(), appO.Name)
		}
		if err != nil {
			return err
		}

	}
	return nil

}
