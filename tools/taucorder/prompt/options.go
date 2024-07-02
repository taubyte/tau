package prompt

import (
	"context"
	"errors"

	"github.com/taubyte/tau/p2p/peer"
)

func Node(n peer.Node) Option {
	return func(p Prompt) error {
		switch _p := p.(type) {
		case *tcprompt:
			_p.node = n
		default:
			return errors.New("unknown prompt type")
		}
		return nil
	}
}

type ScannerHandler func(context.Context, peer.Node) error

func Scanner(sh ScannerHandler) Option {
	return func(p Prompt) error {
		switch _p := p.(type) {
		case *tcprompt:
			_p.scanner = sh
		default:
			return errors.New("unknown prompt type")
		}
		return nil
	}
}
