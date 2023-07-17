package jobs

import (
	"github.com/taubyte/go-project-schema/functions"
	projectLib "github.com/taubyte/go-project-schema/project"
	"github.com/taubyte/go-project-schema/smartops"
	functionSpec "github.com/taubyte/go-specs/function"
	smartOpSpec "github.com/taubyte/go-specs/smartops"
)

func buildTodoFromConfig(projectIface projectLib.Project) ([]Op, error) {
	todos := &todo{
		projectIface: projectIface,
		ops:          make([]Op, 0),
	}
	apps := projectIface.Get().Applications()
	getFunctions := projectIface.Get().Functions
	getSmartOps := projectIface.Get().SmartOps

	// Get Global Functions
	_, functions := getFunctions("")
	for _, f := range functions {
		if err := todos.addFunc(f, ""); err != nil {
			return nil, err
		}
	}

	// Get Global SmartOps
	_, smartOps := getSmartOps("")
	for _, s := range smartOps {
		if err := todos.addSmart(s, ""); err != nil {
			return nil, err
		}
	}

	// Get App Functions and SmartOps
	for _, app := range apps {
		functions, _ = getFunctions(app)
		for _, f := range functions {
			if err := todos.addFunc(f, app); err != nil {
				return nil, err
			}
		}

		smartOps, _ = getSmartOps(app)
		for _, s := range smartOps {
			if err := todos.addSmart(s, app); err != nil {
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

func (t *todo) addSmart(name, app string) error {
	smart, err := t.projectIface.SmartOps(name, app)
	if err != nil {
		return err
	}

	if smart.Get().Source() == "." {
		t.ops = append(t.ops, ToOp(smart))
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
	case smartops.SmartOps:
		getter := obj.Get()
		op.id = getter.Id()
		op.name = getter.Name()
		op.application = getter.Application()
		op.pathVariable = smartOpSpec.PathVariable.String()
	}

	return
}

type todo struct {
	ops          []Op
	projectIface projectLib.Project
}
