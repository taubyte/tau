package services

import (
	commonIface "github.com/taubyte/go-interfaces/common"
)

func (u *Universe) getServiceByNameId(name, id string) (node commonIface.Service, exist bool) {
	u.lock.RLock()
	defer u.lock.RUnlock()
	serviceInfo, exist := u.service[name]
	if !exist {
		return
	}
	if serviceInfo == nil || !exist {
		return
	}

	node, exist = serviceInfo.nodes[id]
	return
}

func (u *Universe) getSimpleByNameId(name, id string) (simple *Simple, exist bool) {
	u.lock.RLock()
	defer u.lock.RUnlock()
	simple, exist = u.simples[name]
	if !exist {
		return
	}
	if simple == nil || !exist {
		return
	}

	return
}
