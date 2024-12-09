package service

import (
	"github.com/taubyte/tau/clients/p2p/auth"
	"github.com/taubyte/tau/clients/p2p/hoarder"
	"github.com/taubyte/tau/clients/p2p/monkey"
	"github.com/taubyte/tau/clients/p2p/patrick"
	"github.com/taubyte/tau/clients/p2p/seer"
	"github.com/taubyte/tau/clients/p2p/tns"
)

func (ni *instance) post() (err error) {
	ni.authClient, err = auth.New(ni.ctx, ni)
	if err != nil {
		return err
	}

	ni.seerClient, err = seer.New(ni.ctx, ni)
	if err != nil {
		return err
	}

	ni.patrickClient, err = patrick.New(ni.ctx, ni)
	if err != nil {
		return err
	}

	ni.hoarderClient, err = hoarder.New(ni.ctx, ni)
	if err != nil {
		return err
	}

	ni.monkeyClient, err = monkey.New(ni.ctx, ni)
	if err != nil {
		return err
	}

	ni.tnsClient, err = tns.New(ni.ctx, ni)
	if err != nil {
		return err
	}

	return nil
}
