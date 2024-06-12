package prompt

import (
	"fmt"
	"os"
	"strings"

	goPrompt "github.com/c-bata/go-prompt"
)

var stringValidator = func(strs ...string) func(Prompt, string, bool) bool {
	return func(p Prompt, w string, exact bool) bool {
		strs = append(strs, "")
		for _, s := range strs {
			if exact {
				if s != "" && s == w {
					return true
				}
			} else {
				if strings.HasPrefix(s, w) {
					return true
				}
			}
		}
		return false
	}
}

var forest = tcforest{
	"/":                  mainTree,
	"/p2p":               p2pTree,
	"/p2p/swarm":         swarmTree,
	"/p2p/discover":      discoverTree,
	"/auth":              authTree,
	"/auth/project":      projectTree,
	"/auth/repo":         repoTree,
	"/auth/acme":         acmeTree,
	"/auth/status":       authStatusTree,
	"/auth/status/db":    authStatusDbTree,
	"/auth/hook":         hooksTree,
	"/hoarder":           hoarderTree,
	"/patrick":           patrickTree,
	"/patrick/status":    patrickStatusTree,
	"/patrick/status/db": patrickStatusDbTree,
	"/seer":              seerTree,
	"/monkey":            monkeyTree,
	"/tns":               tnsTree,
	"/tns/status":        tnsStatusTree,
	"/tns/status/db":     tnsStatusDbTree,
}

var mainTree = &tctree{
	leafs: []*leaf{
		{
			validator: stringValidator("p2p"),
			ret: []goPrompt.Suggest{
				{
					Text:        "p2p",
					Description: "p2p utils",
				},
			},
			jump: func(p Prompt) string {
				return "/p2p"
			},
			handler: func(p Prompt, args []string) error {
				p.SetPath("/p2p")
				return nil
			},
		},
		{
			validator: stringValidator("auth"),
			ret: []goPrompt.Suggest{
				{
					Text:        "auth",
					Description: "auth client",
				},
			},
			jump: func(p Prompt) string {
				return "/auth"
			},
			handler: func(p Prompt, args []string) error {
				p.SetPath("/auth")
				return nil
			},
		},
		{
			validator: stringValidator("hoarder"),
			ret: []goPrompt.Suggest{
				{
					Text:        "hoarder",
					Description: "hoarder client",
				},
			},
			jump: func(p Prompt) string {
				return "/hoarder"
			},
			handler: func(p Prompt, args []string) error {
				p.SetPath("/hoarder")
				return nil
			},
		},
		{
			validator: stringValidator("patrick"),
			ret: []goPrompt.Suggest{
				{
					Text:        "patrick",
					Description: "patrick client",
				},
			},
			jump: func(p Prompt) string {
				return "/patrick"
			},
			handler: func(p Prompt, args []string) error {
				p.SetPath("/patrick")
				return nil
			},
		},
		{
			validator: stringValidator("monkey"),
			ret: []goPrompt.Suggest{
				{
					Text:        "monkey",
					Description: "monkey client",
				},
			},
			jump: func(p Prompt) string {
				return "/monkey"
			},
			handler: func(p Prompt, args []string) error {
				p.SetPath("/monkey")
				return nil
			},
		},
		{
			validator: stringValidator("seer"),
			ret: []goPrompt.Suggest{
				{
					Text:        "seer",
					Description: "seer client",
				},
			},
			jump: func(p Prompt) string {
				return "/seer"
			},
			handler: func(p Prompt, args []string) error {
				p.SetPath("/seer")
				return nil
			},
		},
		{
			validator: stringValidator("tns"),
			ret: []goPrompt.Suggest{
				{
					Text:        "tns",
					Description: "tns client",
				},
			},
			jump: func(p Prompt) string {
				return "/tns"
			},
			handler: func(p Prompt, args []string) error {
				p.SetPath("/tns")
				return nil
			},
		},
		{
			validator: stringValidator("rescan", "refresh"),
			ret: []goPrompt.Suggest{
				{
					Text:        "rescan",
					Description: "rescan for nodes",
				},
				{
					Text:        "refresh",
					Description: "rescan for nodes",
				},
			},
			handler: func(p Prompt, args []string) error {
				switch _p := p.(type) {
				case *tcprompt:
					if _p.scanner == nil {
						panic("no scanner!")
					}
					if err := _p.scanner(_p.ctx, _p.node); err != nil {
						fmt.Println(err)
					}
				default:
					panic("unknown prompt type")
				}
				return nil
			},
		},
		{
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
				fmt.Println("BYE")
				handleExit()
				os.Exit(0)
				return nil
			},
		},
	},
}
