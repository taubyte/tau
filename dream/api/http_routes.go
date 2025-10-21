package api

import httpIface "github.com/taubyte/tau/pkg/http"

func (srv *Service) setUpHttpRoutes() httpIface.Service {
	srv.corsHttp()

	srv.statusHttp()
	srv.universesHttp()
	srv.lesMiesrablesHttp()
	srv.fixtureHttp()
	srv.idHttp()

	srv.injectSimpleHttp()
	srv.injectServiceHttp()
	srv.injectUniverseHttp()

	srv.killServiceHttp()
	srv.killSimpleHttp()
	srv.killNodeIdHttp()
	srv.killUniverseHttp()

	srv.validClients()
	srv.validServices()
	srv.validFixtures()

	srv.pingHttp()

	return srv.server
}
