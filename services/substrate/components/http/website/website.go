package website

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	goHttp "net/http"
	"path"
	"strings"
	"time"

	"github.com/spf13/afero/zipfs"
	"github.com/taubyte/tau/core/services/substrate/components"
	httpComp "github.com/taubyte/tau/core/services/substrate/components/http"
	http "github.com/taubyte/tau/pkg/http"
	matcherSpec "github.com/taubyte/tau/pkg/specs/matcher"
	websiteSpec "github.com/taubyte/tau/pkg/specs/website"
	"github.com/taubyte/tau/services/substrate/components/http/common"
	"github.com/taubyte/tau/services/substrate/components/metrics"
	"github.com/taubyte/tau/utils/readerutil"
)

func (w *Website) Provision() (web httpComp.Serviceable, err error) {
	w.instanceCtx, w.instanceCtxC = context.WithCancel(w.srv.Context())
	w.readyCtx, w.readyCtxC = context.WithCancel(w.srv.Context())
	defer func() {
		w.readyDone = true
		w.readyError = err
		w.readyCtxC()
	}()

	cachedWeb, err := w.srv.Cache().Add(w)
	if err != nil {
		return nil, fmt.Errorf("adding website to cache failed with: %w", err)
	}

	if w != cachedWeb {
		_w, ok := cachedWeb.(httpComp.Website)
		if ok {
			return _w, nil
		}
		// TODO: Debug Logger if this case is met
	}

	if err = w.getAsset(); err != nil {
		return nil, fmt.Errorf("getting website `%s`assets failed with: %w", w.config.Name, err)
	}

	w.metrics.Cached = 1
	w.provisioned = true

	return w, nil
}

func (w *Website) Metrics() *metrics.Website {
	return &w.metrics
}

func (w *Website) Handle(_w goHttp.ResponseWriter, r *goHttp.Request, matcher components.MatchDefinition) (t time.Time, err error) {
	_matcher, ok := matcher.(*common.MatchDefinition)
	if !ok {
		return t, errors.New("invalid match definition")
	}

	pathMatch := _matcher.Get(common.PathMatch)
	_path := cleanRequestPath(r.URL.Path, pathMatch)

	r.URL.Path = _path

	// Server side rendered websites serve immutable assets directly and dispatch
	// every other request (pages and `/api`) to the WebAssembly server bundle.
	if w.isSSR() {
		if assetPath, ok := w.resolveStaticAsset(_path); ok {
			if assetPath == _path {
				// Exact file: the asset handler serves it (ranges, content-type).
				return w.serveStatic(_w, r, false)
			}
			// Clean URL resolved to a directory index (e.g. "/about" ->
			// "/about/index.html"). Serve the file directly: http.FileServer would
			// 301 "/about/index.html" -> "./" to canonicalize, but zipfs has no
			// "/about" directory entry to anchor that, so it loops.
			return w.serveStaticFile(_w, r, assetPath)
		}
		return w.serveSSR(_w, r)
	}

	// Classic static website: serve from the asset with SPA fallback.
	return w.serveStatic(_w, r, true)
}

// cleanRequestPath maps the incoming request path to a path relative to the
// website's mount (pathMatch), preserving a trailing slash except for the root
// (so "/" does not become "//", which a server bundle would fail to route).
func cleanRequestPath(urlPath, pathMatch string) string {
	p := path.Clean("/" + strings.TrimPrefix(urlPath, pathMatch))
	if strings.HasSuffix(urlPath, "/") && p != "/" {
		p += "/"
	}
	return p
}

// serveStatic serves a request straight from the build asset. spa enables the
// single page application fallback (serve index.html for unknown routes), which
// is desirable for static sites but not for SSR ones where unknown routes are
// rendered by the server bundle.
func (w *Website) serveStatic(_w goHttp.ResponseWriter, r *goHttp.Request, spa bool) (time.Time, error) {
	err := w.srv.Http().LowLevelAssetHandler(&http.HeadlessAssetsDefinition{
		FileSystem:            w.root,
		SinglePageApplication: spa,
		Directory:             "/",
	}, _w, r)
	return time.Now(), err
}

// serveStaticFile streams a specific asset file from the build zip, setting the
// content type from its extension (sniffing as a fallback). It is used for clean
// URLs that resolve to a directory index, where http.FileServer's canonical
// "/x/index.html" -> "/x/" redirect would otherwise loop against a zip that has
// no explicit directory entry.
func (w *Website) serveStaticFile(_w goHttp.ResponseWriter, r *goHttp.Request, assetPath string) (time.Time, error) {
	f, err := w.root.Open(assetPath)
	if err != nil {
		return time.Time{}, fmt.Errorf("opening static asset `%s` failed with: %w", assetPath, err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return time.Time{}, fmt.Errorf("stat static asset `%s` failed with: %w", assetPath, err)
	}

	if ctype := mime.TypeByExtension(path.Ext(assetPath)); ctype != "" {
		_w.Header().Set("Content-Type", ctype)
	}

	t := time.Now()
	goHttp.ServeContent(_w, r, path.Base(assetPath), info.ModTime(), f)
	return t, nil
}

func (w *Website) Validate(matcher components.MatchDefinition) error {
	if w.Match(matcher) == matcherSpec.NoMatch {
		return errors.New("no match")
	}

	return nil
}

func (w *Website) getAsset() error {
	dagReader, err := w.srv.Node().GetFile(w.srv.Context(), w.assetId)
	if err != nil {
		return fmt.Errorf("getting build zip failed with: %w", err)
	}

	size, _ := readerutil.Size(dagReader)
	if _, err = dagReader.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("seek start of build zip failed with: %w", err)
	}

	zipReader, err := zip.NewReader(
		readerutil.NewBufferingReaderAt(dagReader),
		size,
	)
	if err != nil {
		return fmt.Errorf("reading build zip failed with: %w", err)
	}

	computedPaths := make([]string, 0)
	assetFiles := make(map[string]struct{})
	manifestDir := websiteSpec.ManifestDir()

	for _, file := range zipReader.File {
		if file.FileInfo().IsDir() {
			continue
		}
		// The internal SSR directory (manifest + server bundle) is not part of
		// the public static surface.
		if isUnderDir(file.Name, manifestDir) {
			continue
		}
		computedPaths = append(computedPaths, file.Name)
		assetFiles[path.Clean("/"+file.Name)] = struct{}{}
	}

	w.computedPaths[w.matcher.Path] = computedPaths
	w.assetFiles = assetFiles
	w.root = zipfs.New(zipReader)

	if err := w.loadManifest(zipReader); err != nil {
		dagReader.Close()
		return fmt.Errorf("loading ssr manifest for website `%s` failed with: %w", w.config.Name, err)
	}

	dagReader.Close()

	return nil
}

// isUnderDir reports whether a (slash separated) file name lives in dir.
func isUnderDir(name, dir string) bool {
	name = strings.TrimPrefix(name, "/")
	return name == dir || strings.HasPrefix(name, dir+"/")
}

func (w *Website) Match(matcher components.MatchDefinition) (currentMatchIndex matcherSpec.Index) {
	currentMatch := matcherSpec.NoMatch
	var pathMatch string
	_matcher, ok := matcher.(*common.MatchDefinition)
	if !ok {
		return
	}

	for _, path := range w.config.Paths {
		// Static websites only answer read methods. Server side rendered
		// websites own their whole path subtree for every method so that `/api`
		// mutations (POST/PUT/DELETE/...) reach the server bundle. An explicitly
		// defined function on the same path still wins via its HighMatch.
		if !w.config.IsSSR() {
			switch _matcher.Method {
			case "", "GET", "HEAD":
			default:
				continue
			}
		}

		matchValue := pathContains(path, _matcher.Path)
		if matchValue > currentMatch {
			pathMatch = path
			currentMatch = matchValue
		}
	}

	if currentMatch >= matcherSpec.MinMatch && currentMatch < matcherSpec.HighMatch {
		computedPaths, ok := w.computedPaths[pathMatch]
		if ok {
			for _, _path := range computedPaths {
				if path.Join(pathMatch, _path) == _matcher.Path {
					currentMatch = matcherSpec.HighMatch
				}
			}
		}
	}

	_matcher.Set(common.PathMatch, pathMatch)

	return currentMatch
}

func pathContains(path, requestPath string) matcherSpec.Index {
	pathLen := len(path)
	reqLen := len(requestPath)

	if pathLen == reqLen && path == requestPath {
		return matcherSpec.HighMatch
	}

	if pathLen == 1 && path == "/" {
		return matcherSpec.MinMatch
	}

	if reqLen < pathLen {
		return matcherSpec.NoMatch
	}

	var score = matcherSpec.DefaultMatch

	// we re add the "/" at the end of path
	path += "/"
	pathLen++

	var i int
	for i = 0; i < pathLen; i++ {
		if requestPath[i] != path[i] {
			return matcherSpec.NoMatch
		}
	}

	score += matcherSpec.Index(i)

	return score
}
