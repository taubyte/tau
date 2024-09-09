package applicationLib

import (
	"github.com/taubyte/tau/pkg/schema/application"
	"github.com/taubyte/tau/pkg/schema/project"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/tools/tau/env"
	projectLib "github.com/taubyte/tau/tools/tau/lib/project"
	"github.com/taubyte/tau/tools/tau/singletons/session"
	"github.com/taubyte/utils/id"
	"github.com/urfave/cli/v2"
)

func SelectedProjectAndApp() (project project.Project, selectedApp string, err error) {
	project, err = projectLib.SelectedProjectInterface()
	if err != nil {
		return
	}

	// Returns a boolean for existence
	selectedApp, _ = env.GetSelectedApplication()

	return
}

func List() ([]string, error) {
	project, err := projectLib.SelectedProjectInterface()
	if err != nil {
		return nil, err
	}

	return project.Get().Applications(), nil
}

func ListResources() ([]*structureSpec.App, error) {
	names, err := List()
	if err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return nil, nil
	}

	resources := make([]*structureSpec.App, len(names))
	for idx, name := range names {
		resources[idx], err = Get(name)
		if err != nil {
			return nil, err
		}
	}

	return resources, nil
}

func Get(name string) (*structureSpec.App, error) {
	project, err := projectLib.SelectedProjectInterface()
	if err != nil {
		return nil, err
	}

	app, err := project.Application(name)
	if err != nil {
		return nil, err
	}
	getter := app.Get()
	return &structureSpec.App{
		Id:          getter.Id(),
		Name:        getter.Name(),
		Description: getter.Description(),
		Tags:        getter.Tags(),
	}, nil
}

func Set(app *structureSpec.App) error {
	project, err := projectLib.SelectedProjectInterface()
	if err != nil {
		return err
	}

	_app, err := project.Application(app.Name)
	if err != nil {
		return err
	}

	return _app.Set(true,
		application.Description(app.Description),
		application.Tags(app.Tags),
	)
}

func Select(ctx *cli.Context, name string) error {
	return env.SetSelectedApplication(ctx, name)
}

func Deselect(ctx *cli.Context, name string) error {
	return session.Unset().SelectedApplication()
}

func New(app *structureSpec.App) error {
	project, err := projectLib.SelectedProjectInterface()
	if err != nil {
		return err
	}

	_app, err := project.Application(app.Name)
	if err != nil {
		return err
	}

	return _app.Set(true,
		application.Id(id.Generate(project.Get().Id(), app.Name)),
		application.Description(app.Description),
		application.Tags(app.Tags),
	)
}

func Delete(app *structureSpec.App) error {
	project, err := projectLib.SelectedProjectInterface()
	if err != nil {
		return err
	}

	_app, err := project.Application(app.Name)
	if err != nil {
		return err
	}

	return _app.Delete()
}
