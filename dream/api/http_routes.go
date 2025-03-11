package api

import httpIface "github.com/taubyte/tau/pkg/http"

func (srv *multiverseService) setUpHttpRoutes() httpIface.Service {
	srv.corsHttp()

	srv.statusHttp()
	srv.universesHttp()
	srv.lesMiesrablesHttp()
	srv.fixtureHttp()
	srv.idHttp()

	// Inject
	srv.injectSimpleHttp()
	srv.injectServiceHttp()
	srv.injectUniverseHttp()

	// Kill
	srv.killServiceHttp()
	srv.killSimpleHttp()
	srv.killNodeIdHttp()
	srv.killUniverseHttp()

	// Get
	srv.validClients()
	srv.validServices()
	srv.validFixtures()

	//ping
	srv.pingHttp()

	return srv.rest
}
