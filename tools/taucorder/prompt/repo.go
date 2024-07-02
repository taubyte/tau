package prompt

import (
	"errors"
	"fmt"
	"strconv"

	goPrompt "github.com/c-bata/go-prompt"
	list "github.com/taubyte/tau/tools/taucorder/helpers"
)

var repoTree = &tctree{
	leafs: []*leaf{
		exitLeaf,
		{
			validator: stringValidator("list"),
			ret: []goPrompt.Suggest{
				{
					Text:        "list",
					Description: "show registered repo ids",
				},
			},
			handler: listRepo,
		},
		{
			validator: stringValidator("get"),
			ret: []goPrompt.Suggest{
				{
					Text:        "get",
					Description: "show a repo's data",
				},
			},
			handler: getRepo,
		},
	},
}

func listRepo(p Prompt, args []string) error {
	repo, err := p.AuthClient().Repositories().Github().List()
	if err != nil {
		return fmt.Errorf("failed listing repos with error: %v", err)
	}

	list.CreateTableIds(repo, "Repo List")

	return nil
}

func getRepo(p Prompt, args []string) error {
	if len(args) < 2 {
		fmt.Println("Must provide repo id")
		return errors.New("must provide repo id")
	}
	rid, err := strconv.Atoi(args[1])
	if err != nil {
		return fmt.Errorf("failed converting %d to int with error: %v", rid, err)
	}

	repo, err := p.AuthClient().Repositories().Github().Get(rid)
	if err != nil {
		return fmt.Errorf("failed getting repo %d with error: %v", rid, err)
	}

	fmt.Println(repo)

	return nil
}
