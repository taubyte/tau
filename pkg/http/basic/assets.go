package basic

import (
	"net/http"
	"path"

	"github.com/spf13/afero"
	service "github.com/taubyte/tau/pkg/http"
	auth "github.com/taubyte/tau/pkg/http/auth"
	"github.com/taubyte/tau/pkg/http/context"
	"github.com/taubyte/tau/pkg/http/request"
)

func (s *Service) ServeAssets(def *service.AssetsDefinition) {
	var fs afero.Fs
	if def.Directory != "" {
		fs = afero.NewBasePathFs(def.FileSystem, def.Directory)
	} else {
		fs = def.FileSystem
	}

	fileServer := http.FileServer(afero.NewHttpFs(fs))

	route := s.Router.PathPrefix(def.Path).HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logger.Debugf("[Asset] %s", r.RequestURI)

		_ctx, err := context.New(&request.Request{ResponseWriter: w, HttpRequest: r}, &def.Vars, context.RawResponse())
		if err != nil {
			// New Context will return error to Client
			logger.Error(err)
			return
		}
		err = _ctx.HandleAuth(auth.Scope(def.Scope, def.Auth.Validator))
		if err != nil {
			// enforceScope will return error to Client
			logger.Error(err)
			return
		}

		defer func() {
			cleanupErr := _ctx.HandleCleanup(def.Auth.GC)
			if err != nil {
				logger.Errorf("cleanup failed with: %s", cleanupErr)
			}
		}()

		// check whether afile exists at the given path
		sts, err := fs.Stat(r.URL.Path)
		if err != nil {
			if def.SinglePageApplication {
				// file does not exist, serve index.html
				r.URL.Path = "/"
			} else {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		} else {
			if sts.IsDir() {
				if def.SinglePageApplication {
					// file does not exist, serve index.html
					r.URL.Path = "/"
				} else {
					w.WriteHeader(http.StatusForbidden)
					return
				}
			}
		}

		if def.BeforeServe != nil {
			def.BeforeServe(w)
		}

		// otherwise, use http.FileServer to serve the static dir
		fileServer.ServeHTTP(w, r)
	})

	if len(def.Host) > 0 {
		route.Host(def.Host)
	}
}

func (s *Service) LowLevelAssetHandler(def *service.HeadlessAssetsDefinition, w http.ResponseWriter, r *http.Request) error {
	var fs afero.Fs
	if len(def.Directory) != 0 {
		fs = afero.NewBasePathFs(def.FileSystem, def.Directory)
	} else {
		fs = def.FileSystem
	}

	fileServer := http.FileServer(afero.NewHttpFs(fs))

	w.Header().Del("Content-Type")

	sts, err := fs.Stat(r.URL.Path)
	if err != nil {
		if def.SinglePageApplication {
			r.URL.Path = "/"
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return nil
		}
	} else {
		_, err := fs.Stat(path.Join(r.URL.Path, "index.html"))
		if sts.IsDir() && err != nil {
			if def.SinglePageApplication {
				r.URL.Path = "/"
			} else {
				w.WriteHeader(http.StatusForbidden)
				return nil
			}
		}
	}

	if def.BeforeServe != nil {
		def.BeforeServe(w)
	}

	fileServer.ServeHTTP(w, r)
	return nil
}

func (s *Service) AssetHandler(def *service.HeadlessAssetsDefinition, ctx service.Context) (interface{}, error) {
	var fs afero.Fs
	if len(def.Directory) != 0 {
		fs = afero.NewBasePathFs(def.FileSystem, def.Directory)
	} else {
		fs = def.FileSystem
	}

	fileServer := http.FileServer(afero.NewHttpFs(fs))

	r := ctx.Request()
	w := ctx.Writer()

	w.Header().Del("Content-Type")
	ctx.SetRawResponse(true)

	sts, err := fs.Stat(r.URL.Path)
	if err != nil {
		if def.SinglePageApplication {
			r.URL.Path = "/"
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return nil, nil
		}
	} else {
		if sts.IsDir() {
			if def.SinglePageApplication {
				r.URL.Path = "/"
			} else {
				w.WriteHeader(http.StatusForbidden)
				return nil, nil
			}
		}
	}

	if def.BeforeServe != nil {
		def.BeforeServe(w)
	}

	fileServer.ServeHTTP(w, r)
	return nil, nil
}
