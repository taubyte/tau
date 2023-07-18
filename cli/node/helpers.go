package node

import (
	"fmt"
	"log"
	"net/http"
	"net/http/pprof"
	"regexp"

	"github.com/taubyte/go-interfaces/moody"
	commonIface "github.com/taubyte/go-interfaces/services/common"
	domainSpecs "github.com/taubyte/go-specs/domain"
)

// TODO: Fix them everything is set to github specs
func setNetworkDomains(conf *commonIface.GenericConfig) {
	domainSpecs.WhiteListedDomains = conf.Domains.Whitelisted.Postfix
	domainSpecs.TaubyteServiceDomain = regexp.MustCompile(conf.Domains.Services)
	domainSpecs.SpecialDomain = regexp.MustCompile(conf.Domains.Generated)
	domainSpecs.TaubyteHooksDomain = regexp.MustCompile(fmt.Sprintf(`https://patrick.tau.%s`, conf.NetworkUrl))
}

// TODO: Eventually expand profiler to other protocols
func startNodeProfiler() {
	go func() {
		logger.Info(moody.Object{"msg": "Starting node profiler"})
		myMux := http.NewServeMux()

		myMux.HandleFunc("/debug/pprof/", pprof.Index)
		myMux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
		myMux.HandleFunc("/debug/pprof/profile", pprof.Profile)
		myMux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
		myMux.HandleFunc("/debug/pprof/trace", pprof.Trace)

		if err := http.ListenAndServe(":11111", myMux); err != nil {
			log.Fatal(fmt.Errorf("error when starting or running http server: %w", err))
		}
	}()
}
