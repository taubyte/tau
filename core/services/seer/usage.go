package seer

import "github.com/taubyte/tau/p2p/streams/command/response"

type Usage interface {
	Beacon(hostname, nodeId, clientNodeId string, signature []byte) UsageBeacon
	Heartbeat(usage *UsageData, hostname, nodeId, clientNodeId string, signature []byte) (response.Response, error)
	Announce(services Services, nodeId, clientNodeId string, signature []byte) (response.Response, error)
	AddService(svrType ServiceType, meta map[string]string)
	List() ([]string, error)
	ListServiceId(name string) ([]string, error)
	Get(id string) (*UsageReturn, error)
}

type UsageBeacon interface {
	Start()
}
