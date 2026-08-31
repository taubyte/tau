package jobs

import (
	"github.com/taubyte/tau/legacy/schema/functions"
	projectLib "github.com/taubyte/tau/legacy/schema/project"
	functionSpec "github.com/taubyte/tau/legacy/specs/function"
)

func buildTodoFromConfig(projectIface projectLib.Project) ([]Op, error) {
	todos := &todo{
		projectIface: projectIface,
		ops:          make([]Op, 0),
	}
	apps := projectIface.Get().Applications()
	getFunctions := projectIface.Get().Functions

	// Get Global Functions
	_, functions := getFunctions("")
	for _, f := range functions {
		if err := todos.addFunc(f, ""); err != nil {
			return nil, err
		}
	}

	// Get App Functions
	for _, app := range apps {
		functions, _ = getFunctions(app)
		for _, f := range functions {
			if err := todos.addFunc(f, app); err != nil {
				return nil, err
			}
		}
	}

	return todos.ops, nil
}

func (t *todo) addFunc(name, app string) error {
	function, err := t.projectIface.Function(name, app)
	if err != nil {
		return err
	}

	if function.Get().Source() == "." {

		t.ops = append(t.ops, ToOp(function))
	}

	return nil
}

// Used in a fixture
func ToOp(value interface{}) (op Op) {
	switch obj := value.(type) {
	case functions.Function:
		getter := obj.Get()
		op.id = getter.Id()
		op.name = getter.Name()
		op.application = getter.Application()
		op.pathVariable = functionSpec.PathVariable.String()
	}

	return
}

type todo struct {
	ops          []Op
	projectIface projectLib.Project
}
