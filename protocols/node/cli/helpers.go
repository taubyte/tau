package main

import (
	"context"
	"errors"
	"time"
)

func waitForSwarm() error {
	wctx, wctx_c := context.WithTimeout(Node.Context(), 5*time.Second)
	defer wctx_c()
	for {
		select {
		case <-time.After(time.Second):
			if len(Node.Peer().Peerstore().Peers()) > 0 {
				return nil
			}
		case <-wctx.Done():
			return errors.New("not able to connect to other peers")
		}

	}
}
