package common

type Patrick interface {
	Done(jid string, cid_log map[string]string) error
	Failed(jid string, cid_log map[string]string) error
}
