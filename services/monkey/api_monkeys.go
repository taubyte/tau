package monkey

import (
	"context"
	"fmt"

	"github.com/taubyte/tau/p2p/streams"
	"github.com/taubyte/tau/p2p/streams/command"
	cr "github.com/taubyte/tau/p2p/streams/command/response"
	"github.com/taubyte/utils/maps"
)

func (m *Service) ServiceHandler(ctx context.Context, conn streams.Connection, body command.Body) (cr.Response, error) {
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
		return m.updateHandler(ctx, jid)
	case "status":
		return m.statusHandler(ctx, jid)
	case "list":
		return m.listHandler()
	case "cancel":
		return m.cancelHandler(jid)
	}

	return nil, nil
}

func (m *Service) listHandler() (cr.Response, error) {
	m.monkeysLock.RLock()
	defer m.monkeysLock.RUnlock()

	ids := make([]string, 0)
	for id := range m.monkeys {
		ids = append(ids, id)
	}
	return cr.Response{"ids": ids}, nil
}

func (m *Service) cancelHandler(jid string) (cr.Response, error) {
	m.monkeysLock.RLock()
	monkey, ok := m.monkeys[jid]
	m.monkeysLock.RUnlock()
	if !ok {
		return nil, fmt.Errorf("Monkey %s does not exist", jid)
	}

	monkey.ctxC()

	m.monkeysLock.Lock()
	delete(m.monkeys, jid)
	m.monkeysLock.Unlock()

	_, err := m.patrickClient.Cancel(jid, monkey.Job.Logs)
	if err != nil {
		return nil, fmt.Errorf("failed patrick client cancel with %w", err)
	}

	return nil, nil
}

// TODO: implement, does nothing...
func (m *Service) updateHandler(ctx context.Context, jid string) (cr.Response, error) {
	m.monkeysLock.RLock()
	defer m.monkeysLock.RUnlock()
	monkey, ok := m.monkeys[jid]
	if !ok {
		return nil, fmt.Errorf("job `%s` not found", jid)
	}

	return cr.Response{"jid": jid, "status": monkey.Status, "logs": monkey.LogCID}, nil
}

func (m *Service) statusHandler(ctx context.Context, jid string) (cr.Response, error) {
	m.monkeysLock.RLock()
	defer m.monkeysLock.RUnlock()

	monkey, ok := m.monkeys[jid]
	if !ok {
		return nil, fmt.Errorf("job `%s` not found", jid)
	}

	return cr.Response{"jid": jid, "status": monkey.Status, "logs": monkey.LogCID}, nil
}
