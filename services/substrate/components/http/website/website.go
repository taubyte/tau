package website

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	goHttp "net/http"
	"path"
	"strings"
	"time"

	"github.com/spf13/afero/zipfs"
	http "github.com/taubyte/http"
	"github.com/taubyte/tau/core/services/substrate/components"
	httpComp "github.com/taubyte/tau/core/services/substrate/components/http"
	matcherSpec "github.com/taubyte/tau/pkg/specs/matcher"
	"github.com/taubyte/tau/services/substrate/components/http/common"
	"github.com/taubyte/tau/services/substrate/components/metrics"
	"go4.org/readerutil"
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
	_path := path.Clean("/" + strings.TrimPrefix(r.URL.Path, pathMatch))
	if strings.HasSuffix(r.URL.Path, "/") {
		_path += "/"
	}

	r.URL.Path = _path
	err = w.srv.Http().LowLevelAssetHandler(&http.HeadlessAssetsDefinition{
		FileSystem:            w.root,
		SinglePageApplication: true,
		Directory:             "/",
	}, _w, r)
	return time.Now(), err
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

	for _, file := range zipReader.File {
		if !file.FileInfo().IsDir() {
			computedPaths = append(computedPaths, file.Name)
		}
	}

	w.computedPaths[w.matcher.Path] = computedPaths
	w.root = zipfs.New(zipReader)
	dagReader.Close()

	return nil
}

func (w *Website) Match(matcher components.MatchDefinition) (currentMatchIndex matcherSpec.Index) {
	currentMatch := matcherSpec.NoMatch
	var pathMatch string
	_matcher, ok := matcher.(*common.MatchDefinition)
	if !ok {
		return
	}

	for _, path := range w.config.Paths {
		switch _matcher.Method {
		case "", "GET", "HEAD":
			matchValue := pathContains(path, _matcher.Path)
			if matchValue > currentMatch {
				pathMatch = path
				currentMatch = matchValue
			}
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
