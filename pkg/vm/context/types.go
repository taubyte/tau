package context

import gocontext "context"

type vmContext struct {
	ctx  gocontext.Context
	ctxC gocontext.CancelFunc

	projectId     string
	applicationId string
	resourceId    string
	branch        string
	commit        string
}
