package common

type Patrick interface {
	Lock(jid string, eta uint32) error
	IsLocked(jid string) (bool, error)
	Done(jid string, cid_log map[string]string) error
	Failed(jid string, cid_log map[string]string) error
}
