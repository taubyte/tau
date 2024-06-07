package prompt

import (
	"fmt"
	"path"

	goPrompt "github.com/c-bata/go-prompt"
)

type tcforest map[string]*tctree

var _ Forest = forest

func (f tcforest) Complete(p Prompt, d goPrompt.Document) []goPrompt.Suggest {
	path := p.CurrentPath()
	tree, ok := f[path]
	if !ok {
		return nil
	}
	return tree.Complete(p, d)
}

func (f tcforest) Execute(p Prompt, in []string) error {
	path := p.CurrentPath()
	tree, ok := f[path]
	if !ok {
		return fmt.Errorf("unknow path `%s`", path)
	}
	return tree.Execute(p, in)
}

func (f tcforest) FindBranch(path string) ([]*leaf, error) {
	tree, ok := f[path]
	if !ok {
		return nil, fmt.Errorf("unknow path `%s`", path)
	}
	return tree.leafs, nil
}

var exitLeaf = &leaf{
	validator: stringValidator("exit", "bye"),
	ret: []goPrompt.Suggest{
		{
			Text:        "exit",
			Description: "exit",
		},
		{
			Text:        "bye",
			Description: "exit",
		},
	},
	handler: func(p Prompt, args []string) error {
		parent, _ := path.Split(p.CurrentPath())
		if parent != "/" && parent[len(parent)-1] == '/' {
			parent = parent[:len(parent)-1]
		}
		p.SetPath(parent)
		return nil
	},
}
