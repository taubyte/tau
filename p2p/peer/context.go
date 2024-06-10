package peer

import "context"

func (p *node) NewChildContextWithCancel() (context.Context, context.CancelFunc) {
	return context.WithCancel(p.ctx)
}
