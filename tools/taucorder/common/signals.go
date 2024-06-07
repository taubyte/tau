package common

import (
	"context"
	"os"
)

var (
	GlobalContext       context.Context
	GlobalContextCancel context.CancelFunc
)

func init() {
	GlobalContext, GlobalContextCancel = context.WithCancel(context.Background())

	go func() {
		select {
		case <-GlobalContext.Done():
			os.Exit(3)
		}
	}()
}

func Exit() {
	GlobalContextCancel()
}
