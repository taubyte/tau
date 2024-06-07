package prompt

import (
	"errors"
	"fmt"

	goPrompt "github.com/c-bata/go-prompt"
	"github.com/ipfs/go-cid"
	list "github.com/taubyte/tau/tools/taucorder/helpers"
)

var hooksTree = &tctree{
	leafs: []*leaf{
		exitLeaf,
		{
			validator: stringValidator("list"),
			ret: []goPrompt.Suggest{
				{
					Text:        "list",
					Description: "show registered hooks",
				},
			},
			handler: hookList,
		},
		{
			validator: stringValidator("get"),
			ret: []goPrompt.Suggest{
				{
					Text:        "get",
					Description: "get repository by hook id",
				},
			},
			handler: getHook,
		},
	},
}

func hookList(p Prompt, args []string) error {
	hooks, err := p.TaubyteAuthClient().Hooks().List()
	if err != nil {
		return fmt.Errorf("failed fetching hooks with error: %v", err)
	}

	if len(hooks) == 0 {
		fmt.Println("No projects are currently stored")
		return nil
	}

	list.CreateTableIds(hooks, "Hooks List")

	return nil
}

func getHook(p Prompt, args []string) error {
	if len(args) < 2 {
		fmt.Println("Must provide hook id")
		return errors.New("must provide PID")
	}
	pid := args[1]
	_, err := cid.Decode(pid)
	if err != nil {
		return fmt.Errorf("project id `%s` is invalid", pid)
	}

	hook, err := p.TaubyteAuthClient().Hooks().Get(pid)
	if err != nil {
		return fmt.Errorf("failed fetching hook `%s` with error: %v", pid, err)
	}

	fmt.Println(hook)

	return nil
}
