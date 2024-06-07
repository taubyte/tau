package prompt

import (
	"context"
	"errors"
	"fmt"
	"time"

	goPrompt "github.com/c-bata/go-prompt"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
)

var discoverTree = &tctree{
	leafs: []*leaf{
		exitLeaf,
		{
			validator: stringValidator("healthy"),
			ret: []goPrompt.Suggest{
				{
					Text:        "healthy",
					Description: "show providers with status check",
				},
			},
			handler: discoverWithCheckCMD,
		},
	},
}

func discoverCMD(p Prompt, args []string) error {
	if len(args) < 2 {
		p.SetPath("/p2p/discover")
		return nil
	}

	service := args[1]

	ctx, ctxC := context.WithTimeout(context.Background(), 3*time.Second)
	defer ctxC()

	peers, err := prompt.Node().Discovery().FindPeers(ctx, service)
	if err != nil {
		fmt.Printf("Failed to discover `%s` with %s\n", service, err.Error())
		return err
	}

	for p := range peers {
		fmt.Printf("- %s %v\n", p.ID.String(), p.Addrs)
	}

	return nil
}

func discoverWithCheckCMD(p Prompt, args []string) error {
	if len(args) < 2 {
		return errors.New("must provide Service")
	}

	service := args[1]

	ctx, ctxC := context.WithTimeout(context.Background(), 5*time.Second)
	defer ctxC()

	peers, err := prompt.Node().Discovery().FindPeers(ctx, service)
	if err != nil {
		fmt.Printf("Failed to discover `%s` with %s\n", service, err.Error())
		return err
	}

	for p := range peers {
		go func(p0 peer.AddrInfo) {
			_ctx, _ctxC := context.WithTimeout(ctx, 300*time.Millisecond)
			defer _ctxC()
			s, err := prompt.Node().Peer().NewStream(_ctx, p0.ID, protocol.ID(service))
			status := "[...]"
			if err != nil {
				status = fmt.Sprintf("[ERROR] %s", err)
			} else {
				status = fmt.Sprintf("[OK|%s]", s.Stat().Direction.String())
				s.Close()
			}
			fmt.Printf("- %s %v %s\n", p0.ID.String(), p0.Addrs, status)
		}(p)
	}

	return nil
}
