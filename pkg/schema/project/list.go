package project

import "github.com/taubyte/tau/pkg/schema/common"

func (g getter) list(application, resourceFolder string) (local []string, global []string) {
	if application != "" {
		local, _ = g.seer.Get(common.ApplicationFolder).Get(application).Get(resourceFolder).List()
	}

	global, _ = g.seer.Get(resourceFolder).List()
	return
}

func (g getter) Libraries(application string) (local []string, global []string) {
	return g.list(application, common.LibraryFolder)
}

func (g getter) Websites(application string) (local []string, global []string) {
	return g.list(application, common.WebsiteFolder)
}

func (g getter) Messaging(application string) (local []string, global []string) {
	return g.list(application, common.MessagingFolder)
}

func (g getter) Databases(application string) (local []string, global []string) {
	return g.list(application, common.DatabaseFolder)
}

func (g getter) Storages(application string) (local []string, global []string) {
	return g.list(application, common.StorageFolder)
}

func (g getter) Services(application string) (local []string, global []string) {
	return g.list(application, common.ServiceFolder)
}

func (g getter) Domains(application string) (local []string, global []string) {
	return g.list(application, common.DomainFolder)
}

func (g getter) SmartOps(application string) (local []string, global []string) {
	return g.list(application, common.SmartOpsFolder)
}

func (g getter) Functions(application string) (local []string, global []string) {
	return g.list(application, common.FunctionFolder)
}
