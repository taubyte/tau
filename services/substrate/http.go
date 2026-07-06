package substrate

import (
	"net"
	"net/http"
	"net/url"
	"strconv"

	"github.com/taubyte/tau/pkg/config"
	"github.com/taubyte/tau/services/common/httpsvc"
)

func (srv *Service) startHttp(cfg config.Config) (err error) {
	listen := cfg.HttpListen()

	if srv.http = cfg.Http(); srv.http == nil {
		srv.http, err = httpsvc.New(srv.ctx, srv.node, cfg)
		if err != nil {
			return err
		}
	}

	if !cfg.DevMode() {
		host, port, err := net.SplitHostPort(listen)
		if err != nil {
			return err
		}

		_port, err := strconv.Atoi(port)
		if err != nil {
			return err
		}

		if _port == 443 {
			_port = 80
		} else {
			_port++
		}

		listen = net.JoinHostPort(host, strconv.Itoa(_port))

		go func() {
			err := http.ListenAndServe(listen, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				///original from: https://gist.github.com/d-schmidt/587ceec34ce1334a5e60
				target := url.URL{Scheme: "https", Host: r.Host, Path: r.URL.Path, RawQuery: r.URL.RawQuery}
				http.Redirect(w, r, target.String(),
					// see comments below and consider the codes 308, 302, or 301
					http.StatusTemporaryRedirect)
			}))
			if err != nil {
				logger.Errorf("http to https redirect server on %s failed with: %s", listen, err.Error())
			}
		}()
	}

	return
}
