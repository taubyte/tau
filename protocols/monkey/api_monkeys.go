package monkey

import (
	"context"
	"fmt"

	"github.com/taubyte/p2p/streams"
	"github.com/taubyte/p2p/streams/command"
	cr "github.com/taubyte/p2p/streams/command/response"
	"github.com/taubyte/utils/maps"
)

func (bob *Service) ServiceHandler(ctx context.Context, conn streams.Connection, body command.Body) (cr.Response, error) {
	action, err := maps.String(body, "action")
	if err != nil {
		return nil, err
	}

	jid, err := maps.String(body, "jid")
	if err != nil {
		jid = ""
	}
	if len(jid) == 0 && action != "list" {
		return nil, fmt.Errorf("jid:(Job Id) not provided")
	}
	switch action {
	case "update":
		return bob.updateHandler(ctx, jid)
	case "status":
		return bob.statusHandler(ctx, jid)
	case "list":
		return bob.listHandler()
	case "cancel":
		return bob.cancelHandler(jid)
	}

	return nil, nil
}

func (bob *Service) listHandler() (cr.Response, error) {
	ids := make([]string, 0)
	for id := range bob.monkeys {
		ids = append(ids, id)
	}
	return cr.Response{"ids": ids}, nil
}

func (bob *Service) cancelHandler(jid string) (cr.Response, error) {
	monkey, ok := bob.monkeys[jid]
	if !ok {
		return nil, fmt.Errorf("Monkey %s does not exist", jid)
	}

	monkey.ctxC()
	delete(bob.monkeys, jid)
	_, err := bob.patrickClient.Cancel(jid, monkey.Job.Logs)
	if err != nil {
		return nil, fmt.Errorf("failed patrick client cancel with %w", err)
	}

	return nil, nil
}

// TODO: implement, does nothing...
func (bob *Service) updateHandler(ctx context.Context, jid string) (cr.Response, error) {
	monkey, ok := bob.monkeys[jid]
	if !ok {
		return nil, fmt.Errorf("job `%s` not found", jid)
	}
	return cr.Response{"jid": jid, "status": monkey.Status, "logs": monkey.LogCID}, nil
}

func (bob *Service) statusHandler(ctx context.Context, jid string) (cr.Response, error) {
	monkey, ok := bob.monkeys[jid]
	if !ok {
		return nil, fmt.Errorf("job `%s` not found", jid)
	}
	return cr.Response{"jid": jid, "status": monkey.Status, "logs": monkey.LogCID}, nil
}
