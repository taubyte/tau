package prompt

import (
	"errors"
	"fmt"

	goPrompt "github.com/c-bata/go-prompt"
	"github.com/ipfs/go-cid"
	list "github.com/taubyte/tau/tools/taucorder/helpers"
)

var projectTree = &tctree{
	leafs: []*leaf{
		exitLeaf,
		{
			validator: stringValidator("list"),
			ret: []goPrompt.Suggest{
				{
					Text:        "list",
					Description: "show registered project ids",
				},
			},
			handler: listProjects,
		},
		{
			validator: stringValidator("get"),
			ret: []goPrompt.Suggest{
				{
					Text:        "get",
					Description: "show a project's data",
				},
			},
			handler: getProject,
		},
	},
}

func listProjects(p Prompt, args []string) error {
	prj, err := p.TaubyteAuthClient().Projects().List()
	if err != nil {
		return fmt.Errorf("failed listing repos with error: %v", err)
	}

	if len(prj) == 0 {
		fmt.Println("No projects are currently stored")
		return nil
	}

	list.CreateTableIds(prj, "Project Id's")

	return nil
}
func getProject(p Prompt, args []string) error {
	if len(args) < 2 {
		fmt.Println("Must provide project id")
		return errors.New("must provide project id")
	}
	pid := args[1]
	_, err := cid.Decode(pid)
	if err != nil {
		return fmt.Errorf("project id `%s` is invalid", pid)
	}

	prj := p.TaubyteAuthClient().Projects().Get(pid)
	if prj == nil {
		return fmt.Errorf("failed fetching project `%s`", pid)
	}

	fmt.Println(prj)

	return nil
}
