package prompt

import (
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/taubyte/tau/core/services/tns"

	goPrompt "github.com/c-bata/go-prompt"
	spec "github.com/taubyte/tau/pkg/specs/common"
	list "github.com/taubyte/tau/tools/taucorder/helpers"
)

var tnsTree = &tctree{
	leafs: []*leaf{
		exitLeaf,
		{
			validator: stringValidator("list"),
			ret: []goPrompt.Suggest{
				{
					Text:        "list",
					Description: "show all keys registered",
				},
			},
			handler: listKeys,
		},
		{
			validator: stringValidator("fetch"),
			ret: []goPrompt.Suggest{
				{
					Text:        "fetch",
					Description: "fetch a key",
				},
			},
			handler: fetchValue,
		},
		{
			validator: stringValidator("lookup"),
			ret: []goPrompt.Suggest{
				{
					Text:        "lookup",
					Description: "lookup a key",
				},
			},
			handler: lookupValue,
		},
		{
			validator: stringValidator("status"),
			ret: []goPrompt.Suggest{
				{
					Text:        "status",
					Description: "request status",
				},
			},
			jump: func(p Prompt) string {
				return "/tns/status"
			},
			handler: func(p Prompt, args []string) error {
				p.SetPath("/tns/status")
				return nil
			},
		},
	},
}

func listKeys(p Prompt, args []string) error {
	keys, err := p.TnsClient().List(1)
	if err != nil {
		return fmt.Errorf("failed listing tns keys with error: %w", err)
	}

	if len(keys) == 0 {
		fmt.Println("No keys are currently stored")
		return nil
	}

	list.CreateTableIds(keys, "Keys List")

	return nil
}

func fetchValue(p Prompt, args []string) error {
	if len(args) == 1 {
		return errors.New("no arguments provided to fetch")
	}

	iface, err := p.TnsClient().Fetch(spec.NewTnsPath([]string{args[1]}))
	if err != nil {
		return fmt.Errorf("failed listing tns keys with error: %w", err)
	}

	return list.CreateTableInterface("Fetch", iface.Interface())
}

func lookupValue(p Prompt, args []string) error {
	if len(args) == 1 {
		return errors.New("no arguments provided to lookup")
	}

	iface, err := p.TnsClient().Lookup(tns.Query{Prefix: args[1:]})
	if err != nil {
		return fmt.Errorf("failed listing tns keys with error: %w", err)
	}

	_iface := make([]string, 0)
	for _, item := range iface.([]string) {
		start := path.Join(args[1:]...)
		if !strings.HasPrefix(start, "/") {
			start = "/" + start
		}
		_iface = append(_iface, strings.TrimPrefix(item, start))
	}

	return list.CreateTableInterface("Lookup", _iface)
}
