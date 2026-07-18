package schema

import (
	"encoding/json"
	"testing"

	_ "embed"

	"github.com/taubyte/tau/pkg/tcc/engine"
	yaseer "github.com/taubyte/tau/pkg/yaseer"
	"gotest.tools/v3/assert"
)

func TestSchema(t *testing.T) {
	taubyteJson, err := TaubyteProject.Json()
	assert.NilError(t, err)

	var parserData interface{}

	err = json.Unmarshal([]byte(taubyteJson), &parserData)
	if err != nil {
		t.Fatalf("Failed to unmarshal embedded parser JSON: %v", err)
	}
}

func TestConfigSchema(t *testing.T) {
	p, err := engine.New(TaubyteProject, yaseer.SystemFS("../fixtures/config"))
	assert.NilError(t, err)

	obj, err := p.Parse()
	assert.NilError(t, err)

	appObj, err := obj.Child("applications").Object()
	assert.NilError(t, err)

	app1Obj, err := appObj.Child("test_app1").Object()
	assert.NilError(t, err)

	app2Obj, err := appObj.Child("test_app2").Object()
	assert.NilError(t, err)

	app1funcsObj, err := app1Obj.Child("functions").Object()
	assert.NilError(t, err)

	app2funcsObj, err := app2Obj.Child("functions").Object()
	assert.NilError(t, err)

	funcsObj, err := obj.Child("functions").Object()
	assert.NilError(t, err)

	app1funcs2Obj, err := app1funcsObj.Child("test_function2").Object()
	assert.NilError(t, err)

	app2funcs2Obj, err := app2funcsObj.Child("test_function2").Object()
	assert.NilError(t, err)

	funcs2Obj, err := funcsObj.Child("test_function2_glob").Object()
	assert.NilError(t, err)

	assert.DeepEqual(t, app1funcs2Obj.Map(), app2funcs2Obj.Map())
	assert.DeepEqual(t, funcs2Obj.Map(), app2funcs2Obj.Map())

}
