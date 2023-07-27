package services

import (
	"fmt"

	peer "github.com/taubyte/p2p/peer"
)

func (u *Universe) Kill(name string) error {
	var isService bool
	for _, service := range ValidServices() {
		if name == service {
			isService = true
			break
		}
	}

	if isService {
		ids, err := u.GetServicePids(name)
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			return fmt.Errorf("killing %s failed with: does not exist", name)
		}

		return u.killServiceByNameId(name, ids[0])

	} else {
		u.lock.RLock()
		simple, exist := u.simples[name]
		u.lock.RUnlock()
		if !exist {
			return fmt.Errorf("killing %s failed with: does not exist", name)
		}

		return u.killSimpleByNameId(name, simple.ID().Pretty())
	}
}

func (u *Universe) discardNode(node peer.Node, lock bool) {
	// ref: https://stackoverflow.com/questions/20545743/how-to-remove-items-from-a-slice-while-ranging-over-it
	if lock {
		u.lock.Lock()
		defer u.lock.Unlock()
	}

	for i := len(u.all) - 1; i >= 0; i-- {
		if u.all[i] == node {
			u.all = append(u.all[:i], u.all[i+1:]...)
		}
	}
}
