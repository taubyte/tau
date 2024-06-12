package prompt

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	goPrompt "github.com/c-bata/go-prompt"
	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/libp2p/go-libp2p/core/discovery"
	peerCore "github.com/libp2p/go-libp2p/core/peer"
	"github.com/taubyte/tau/services/common"
)

var authStatusTree = &tctree{
	leafs: []*leaf{
		exitLeaf,
		{
			validator: stringValidator("db"),
			ret: []goPrompt.Suggest{
				{
					Text:        "db",
					Description: "show database stats",
				},
			},
			jump: func(p Prompt) string {
				return "/auth/status/db"
			},
			handler: func(p Prompt, args []string) error {
				s, err := p.AuthClient().Stats().Database()
				if err != nil {
					return err
				}

				if len(s.Heads()) == 0 {
					fmt.Println("Database is empty.")
					return nil
				}

				t := table.NewWriter()
				t.SetStyle(table.StyleLight)
				t.SetOutputMirror(os.Stdout)

				for _, hcid := range s.Heads() {
					t.AppendRows([]table.Row{
						{"Heads", hcid.String()},
					}, table.RowConfig{AutoMerge: true})
				}

				t.AppendSeparator()

				t.Render()

				return nil
			},
		},
	},
}

var authStatusDbTree = &tctree{
	leafs: []*leaf{
		{
			validator: stringValidator("all", "*"),
			ret: []goPrompt.Suggest{
				{
					Text:        "all",
					Description: "show database stats of all nodes",
				},
				{
					Text:        "*",
					Description: "show database stats of all nodes",
				},
			},
			handler: func(p Prompt, args []string) error {
				ctx, ctxC := context.WithTimeout(context.Background(), 60*time.Second)
				defer ctxC()

				peers, err := prompt.Node().Discovery().FindPeers(ctx, common.AuthProtocol, discovery.Limit(1024))
				if err != nil {
					fmt.Printf("Failed to discover `auth` with %s\n", err.Error())
					return err
				}

				t := table.NewWriter()
				t.SetStyle(table.StyleLight)
				t.SetOutputMirror(os.Stdout)
				t.AppendHeader(table.Row{"Node", "Heads"})

				for peer := range peers {
					s, err := p.AuthClient().Peers(peer.ID).Stats().Database()
					if err != nil {
						t.AppendRows([]table.Row{
							{peer.ID.String(), err.Error()},
						})
					} else if len(s.Heads()) == 0 {
						t.AppendRows([]table.Row{
							{peer.ID.String(), "Empty"},
						})
					} else {
						for _, hcid := range s.Heads() {
							t.AppendRows([]table.Row{
								{peer.ID.String(), hcid.String()},
							}, table.RowConfig{AutoMerge: true})
						}
					}
					t.AppendSeparator()
				}

				t.Render()

				return nil
			},
		},
		{
			validator: stringValidator("node", "of"),
			ret: []goPrompt.Suggest{
				{
					Text:        "node",
					Description: "show database stats of a nodes",
				},
				{
					Text:        "of",
					Description: "show database stats of a nodes",
				},
			},
			handler: func(p Prompt, args []string) error {
				if len(args) != 2 {
					fmt.Println("You need to provide PID")
					return errors.New("missing pid")
				}

				pid, err := peerCore.Decode(args[1])
				if err != nil {
					fmt.Println("can't parse PID")
					return err
				}

				s, err := p.AuthClient().Peers(pid).Stats().Database()
				if err != nil {
					fmt.Println(err)
					return err
				}

				if len(s.Heads()) == 0 {
					fmt.Println("Database is empty.")
					return nil
				}

				t := table.NewWriter()
				t.SetStyle(table.StyleLight)
				t.SetOutputMirror(os.Stdout)

				for _, hcid := range s.Heads() {
					t.AppendRows([]table.Row{
						{"Heads", hcid.String()},
					}, table.RowConfig{AutoMerge: true})
				}

				t.AppendSeparator()

				t.Render()

				return nil
			},
		},
	},
}
