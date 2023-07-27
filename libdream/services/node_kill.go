package services

import (
	"fmt"

	"golang.org/x/exp/slices"
)

func (u *Universe) killServiceByNameId(name, id string) error {
	node, exist := u.getServiceByNameId(name, id)
	if !exist {
		return fmt.Errorf("killing %s: %s failed with: does not exist", name, id)
	}

	serviceInfo, exist := u.service[name]
	if !exist {
		return fmt.Errorf("killing %s: %s failed with: does not exist", name, id)
	}

	u.lock.Lock()
	defer u.lock.Unlock()
	node.Close()
	delete(serviceInfo.nodes, id)
	delete(u.lookups, id)
	u.discardNode(node.Node(), false)

	return nil
}

func (u *Universe) killSimpleByNameId(name, id string) error {
	simple, exist := u.getSimpleByNameId(name, id)
	if !exist {
		return fmt.Errorf("killing %s: %s failed with: does not exist", name, id)
	}

	u.lock.Lock()
	defer u.lock.Unlock()
	simple.Close()
	delete(u.simples, name)
	delete(u.lookups, id)
	u.discardNode(simple.PeerNode(), false)

	return nil
}

func (u *Universe) KillNodeByNameID(name, id string) error {
	if slices.Contains(ValidServices(), name) {
		return u.killServiceByNameId(name, id)
	} else {
		return u.killSimpleByNameId(name, id)
	}
}
