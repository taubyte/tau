package project

import (
	"fmt"

	httpClient "github.com/taubyte/tau/clients/http/auth"
	auth_client "github.com/taubyte/tau/tools/tau/clients/auth_client"
	projectLib "github.com/taubyte/tau/tools/tau/lib/project"
	"github.com/taubyte/tau/tools/tau/output"
	"github.com/taubyte/tau/tools/tau/prompts/spinner"
	projectTable "github.com/taubyte/tau/tools/tau/table/project"
	"github.com/urfave/cli/v2"
)

func list(ctx *cli.Context) error {
	client, err := auth_client.Load()
	if err != nil {
		return err
	}

	stopGlobe := spinner.Globe()
	projects, err := client.Projects()
	if err != nil {
		return fmt.Errorf("Query projects failed with %s", err.Error())
	}
	stopGlobe()

	if output.Render(projects) {
		return nil
	}

	t := projectTable.ListNoRender(projects, func(project *httpClient.Project) string {
		return projectLib.Description(project)
	})

	t.Render()

	return nil
}
