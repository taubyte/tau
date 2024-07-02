package prompt

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	goPrompt "github.com/c-bata/go-prompt"
	"github.com/libp2p/go-libp2p/core/discovery"
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

	ctx, ctxC := context.WithTimeout(context.Background(), 30*time.Second)
	defer ctxC()

	peers, err := prompt.Node().Discovery().FindPeers(ctx, service, discovery.Limit(1024))
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

	ctx, ctxC := context.WithTimeout(context.Background(), 30*time.Second)
	defer ctxC()

	peers, err := prompt.Node().Discovery().FindPeers(ctx, service, discovery.Limit(1024))
	if err != nil {
		fmt.Printf("Failed to discover `%s` with %s\n", service, err.Error())
		return err
	}

	var wg sync.WaitGroup
	for p := range peers {
		wg.Add(1)
		go func(p0 peer.AddrInfo) {
			defer wg.Done()
			_ctx, _ctxC := context.WithTimeout(ctx, 3*time.Second)
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

	wg.Wait()

	return nil
}
