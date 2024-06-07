package prompt

import goPrompt "github.com/c-bata/go-prompt"

var p2pTree = &tctree{
	leafs: []*leaf{
		exitLeaf,
		{
			validator: stringValidator("ping"),
			ret: []goPrompt.Suggest{
				{
					Text:        "ping",
					Description: "ping a node",
				},
			},
			handler: pingCMD,
		},
		{
			validator: stringValidator("swarm"),
			ret: []goPrompt.Suggest{
				{
					Text:        "swarm",
					Description: "show swarm",
				},
			},
			jump: func(p Prompt) string {
				return "/p2p/swarm"
			},
			handler: func(p Prompt, args []string) error {
				p.SetPath("/p2p/swarm")
				return nil
			},
		},
		{
			validator: stringValidator("discover", "find"),
			ret: []goPrompt.Suggest{
				{
					Text:        "discover",
					Description: "discover a service",
				},
				{
					Text:        "find",
					Description: "find a service",
				},
			},
			jump: func(p Prompt) string {
				return "/p2p/discover"
			},
			handler: discoverCMD,
		},
	},
}
