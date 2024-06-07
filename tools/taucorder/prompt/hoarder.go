package prompt

import (
	"fmt"

	goPrompt "github.com/c-bata/go-prompt"
	list "github.com/taubyte/tau/tools/taucorder/helpers"
)

var hoarderTree = &tctree{
	leafs: []*leaf{
		exitLeaf,
		{
			validator: stringValidator("list"),
			ret: []goPrompt.Suggest{
				{
					Text:        "list",
					Description: "show all stashed cids",
				},
			},
			handler: listCids,
		},
	},
}

func listCids(p Prompt, args []string) error {
	ids, err := p.TaubyteHoarderClient().List()
	if err != nil {
		return fmt.Errorf("failed listing hoarder cids with error: %w", err)
	}

	if len(ids) == 0 {
		fmt.Println("No ids are currently stored")
		return nil
	}

	list.CreateTableIds(ids, "Hoarder List")

	return nil
}
