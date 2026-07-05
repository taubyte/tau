package accounts

import (
	"context"
	"fmt"
	"time"

	accountsIface "github.com/taubyte/tau/core/services/accounts"
	"github.com/taubyte/tau/p2p/streams/command"
)

// prefsImpl is the P2P client for one Account's PRef surface.
type prefsImpl struct {
	c         *Client
	accountID string
}

func (i *prefsImpl) Create(ctx context.Context, in accountsIface.CreatePRefInput) (*accountsIface.PRef, error) {
	resp, err := i.c.client.Send(verbPRef, command.Body{
		"action":       "create",
		"account_id":   i.accountID,
		"name":         in.Name,
		"display_name": in.DisplayName,
		"member_id":    in.MemberID,
	}, i.c.peers...)
	if err != nil {
		return nil, fmt.Errorf("prefs.Create: %w", err)
	}
	var pref accountsIface.PRef
	if err := readField(resp, "pref", &pref); err != nil {
		return nil, err
	}
	return &pref, nil
}

func (i *prefsImpl) Get(ctx context.Context, name string) (*accountsIface.PRef, error) {
	resp, err := i.c.client.Send(verbPRef, command.Body{
		"action":     "get",
		"account_id": i.accountID,
		"name":       name,
	}, i.c.peers...)
	if err != nil {
		return nil, fmt.Errorf("prefs.Get: %w", err)
	}
	var pref accountsIface.PRef
	if err := readField(resp, "pref", &pref); err != nil {
		return nil, err
	}
	return &pref, nil
}

func (i *prefsImpl) List(ctx context.Context) ([]string, error) {
	resp, err := i.c.client.Send(verbPRef, command.Body{
		"action":     "list",
		"account_id": i.accountID,
	}, i.c.peers...)
	if err != nil {
		return nil, fmt.Errorf("prefs.List: %w", err)
	}
	var names []string
	if err := readField(resp, "names", &names); err != nil {
		return nil, err
	}
	return names, nil
}

func (i *prefsImpl) SetDisplayName(ctx context.Context, name, displayName string) (*accountsIface.PRef, error) {
	resp, err := i.c.client.Send(verbPRef, command.Body{
		"action":       "set-display-name",
		"account_id":   i.accountID,
		"name":         name,
		"display_name": displayName,
	}, i.c.peers...)
	if err != nil {
		return nil, fmt.Errorf("prefs.SetDisplayName: %w", err)
	}
	var pref accountsIface.PRef
	if err := readField(resp, "pref", &pref); err != nil {
		return nil, err
	}
	return &pref, nil
}

func (i *prefsImpl) Assign(ctx context.Context, in accountsIface.AssignPRefInput) (*accountsIface.PRefEvent, error) {
	resp, err := i.c.client.Send(verbPRef, command.Body{
		"action":     "assign",
		"account_id": i.accountID,
		"name":       in.Name,
		"plan_id":    in.PlanID,
		"member_id":  in.MemberID,
		"note":       in.Note,
	}, i.c.peers...)
	if err != nil {
		return nil, fmt.Errorf("prefs.Assign: %w", err)
	}
	var ev accountsIface.PRefEvent
	if err := readField(resp, "event", &ev); err != nil {
		return nil, err
	}
	return &ev, nil
}

func (i *prefsImpl) Disable(ctx context.Context, in accountsIface.DisablePRefInput) (*accountsIface.PRefEvent, error) {
	resp, err := i.c.client.Send(verbPRef, command.Body{
		"action":     "disable",
		"account_id": i.accountID,
		"name":       in.Name,
		"member_id":  in.MemberID,
		"note":       in.Note,
	}, i.c.peers...)
	if err != nil {
		return nil, fmt.Errorf("prefs.Disable: %w", err)
	}
	var ev accountsIface.PRefEvent
	if err := readField(resp, "event", &ev); err != nil {
		return nil, err
	}
	return &ev, nil
}

func (i *prefsImpl) Enable(ctx context.Context, in accountsIface.EnablePRefInput) (*accountsIface.PRefEvent, error) {
	resp, err := i.c.client.Send(verbPRef, command.Body{
		"action":     "enable",
		"account_id": i.accountID,
		"name":       in.Name,
		"member_id":  in.MemberID,
		"note":       in.Note,
	}, i.c.peers...)
	if err != nil {
		return nil, fmt.Errorf("prefs.Enable: %w", err)
	}
	var ev accountsIface.PRefEvent
	if err := readField(resp, "event", &ev); err != nil {
		return nil, err
	}
	return &ev, nil
}

func (i *prefsImpl) Events(ctx context.Context, name string, from, to time.Time) ([]accountsIface.PRefEvent, error) {
	body := command.Body{
		"action":     "events",
		"account_id": i.accountID,
		"name":       name,
	}
	if !from.IsZero() {
		body["from_unixnano"] = from.UnixNano()
	}
	if !to.IsZero() {
		body["to_unixnano"] = to.UnixNano()
	}
	resp, err := i.c.client.Send(verbPRef, body, i.c.peers...)
	if err != nil {
		return nil, fmt.Errorf("prefs.Events: %w", err)
	}
	var events []accountsIface.PRefEvent
	if err := readField(resp, "events", &events); err != nil {
		return nil, err
	}
	return events, nil
}

func (i *prefsImpl) LatestEvent(ctx context.Context, name string) (*accountsIface.PRefEvent, error) {
	events, err := i.Events(ctx, name, time.Time{}, time.Time{})
	if err != nil {
		return nil, err
	}
	if len(events) == 0 {
		return nil, fmt.Errorf("prefs.LatestEvent: no events for %q", name)
	}
	return &events[len(events)-1], nil
}
