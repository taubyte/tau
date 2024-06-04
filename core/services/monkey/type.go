package monkey

import "github.com/taubyte/tau/core/services/patrick"

type StatusResponse struct {
	Jid    string
	Status patrick.JobStatus
	Logs   string
}
