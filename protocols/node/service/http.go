package service

import (
	"net"
	"net/http"
	"net/url"
	"strconv"

	"github.com/taubyte/odo/config"
	auto "github.com/taubyte/odo/pkgs/http-auto"
)

func (srv *Service) startHttp(config *config.Protocol) (err error) {
	listen := config.HttpListen

	if config.Http == nil {
		srv.http, err = auto.Configure(config).AutoHttp(srv.node)
		if err != nil {
			return err
		}
	} else {
		srv.http = config.Http
	}

	if !config.DevMode {
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
				panic(err)
			}
		}()
	}

	return
}
