package prompt

import (
	"fmt"
	"strings"

	goPrompt "github.com/c-bata/go-prompt"

	"github.com/google/shlex"
)

type leaf struct {
	validator func(Prompt, string, bool) bool
	handler   func(p Prompt, args []string) error
	jump      func(p Prompt) string
	ret       []goPrompt.Suggest
	leafs     []*leaf
}

type tctree struct {
	leafs []*leaf
}

var _ Tree = mainTree

func (c tctree) Complete(p Prompt, d goPrompt.Document) []goPrompt.Suggest {
	blocks, err := shlex.Split(d.Text)
	if err != nil {
		blocks = strings.Split(strings.TrimSpace(d.Text), " ")
	}
	if !strings.HasSuffix(d.Text, " ") && len(blocks) > 1 {
		blocks = blocks[:len(blocks)-1]
	}
	tree := c.leafs
	ret := make([]goPrompt.Suggest, 0)
	if len(blocks) > 0 {
		for _, b := range blocks {
			if len(b) > 0 && (b[0] == '"' || b[0] == '\'') {
				b = b[1:]
			}
			if tree == nil {
				// reached a dead-end
				// maybe not
				return nil
			}
			for _, s := range tree {
				if s.validator == nil || s.validator(p, b, true) {
					if s.leafs != nil {
						tree = s.leafs
					} else if s.jump != nil {
						newpath := s.jump(p)
						tree, _ = forest.FindBranch(newpath)
					}
				}
			}
		}
	}
	b := d.GetWordBeforeCursor()
	if len(b) > 0 && (b[0] == '"' || b[0] == '\'') {
		b = b[1:]
	}
	for _, s := range tree {
		if s.validator == nil || s.validator(p, b, false) {
			ret = append(ret, s.ret...)
		}
	}

	return ret
}

func (c tctree) Execute(p Prompt, in []string) error {
	tree := c.leafs
	for i, b := range in {
		for _, s := range tree {
			if s.validator == nil || s.validator(p, b, true) {
				if s.leafs == nil && s.jump != nil && i+1 < len(in) {
					newpath := s.jump(p)
					tree, _ = forest.FindBranch(newpath)
					continue
				}
				if s.leafs == nil || i+1 == len(in) {
					if s.handler == nil {
						return fmt.Errorf("no handler for `%s`", b)
					}
					return s.handler(prompt, in[i:])
				}
				tree = s.leafs
			}
		}
	}

	return fmt.Errorf("can't find match for `%v`", in)
}
