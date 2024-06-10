package prompt

import (
	goPrompt "github.com/c-bata/go-prompt"
)

var authTree = &tctree{
	leafs: []*leaf{
		exitLeaf,
		{
			validator: stringValidator("project"),
			ret: []goPrompt.Suggest{
				{
					Text:        "project",
					Description: "show project options",
				},
			},
			jump: func(p Prompt) string {
				return "/auth/project"
			},
			handler: func(p Prompt, args []string) error {
				p.SetPath("/auth/project")
				return nil
			},
		},
		{
			validator: stringValidator("hook"),
			ret: []goPrompt.Suggest{
				{
					Text:        "hook",
					Description: "show hook options",
				},
			},
			jump: func(p Prompt) string {
				return "/auth/hook"
			},
			handler: func(p Prompt, args []string) error {
				p.SetPath("/auth/hook")
				return nil
			},
		},
		{
			validator: stringValidator("repo"),
			ret: []goPrompt.Suggest{
				{
					Text:        "repo",
					Description: "show repo options",
				},
			},
			jump: func(p Prompt) string {
				return "/auth/repo"
			},
			handler: func(p Prompt, args []string) error {
				p.SetPath("/auth/repo")
				return nil
			},
		},
		{
			validator: stringValidator("acme"),
			ret: []goPrompt.Suggest{
				{
					Text:        "acme",
					Description: "show acme options",
				},
			},
			jump: func(p Prompt) string {
				return "/auth/acme"
			},
			handler: func(p Prompt, args []string) error {
				p.SetPath("/auth/acme")
				return nil
			},
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
				return "/auth/status"
			},
			handler: func(p Prompt, args []string) error {
				p.SetPath("/auth/status")
				return nil
			},
		},
	},
}
