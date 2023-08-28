package website

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	goHttp "net/http"
	"path"
	"strings"
	"time"

	"github.com/ipfs/go-cid"
	"github.com/spf13/afero/zipfs"
	commonIface "github.com/taubyte/go-interfaces/services/substrate/components"
	matcherSpec "github.com/taubyte/go-specs/matcher"
	"github.com/taubyte/go-specs/methods"
	structureSpec "github.com/taubyte/go-specs/structure"
	http "github.com/taubyte/http"
	"github.com/taubyte/tau/protocols/substrate/components/http/common"
	"go4.org/readerutil"
)

func (w *Website) Project() (cid.Cid, error) {
	return cid.Decode(w.project)
}

func (w *Website) Handle(_w goHttp.ResponseWriter, r *goHttp.Request, matcher commonIface.MatchDefinition) (t time.Time, err error) {
	_matcher, ok := matcher.(*common.MatchDefinition)
	if !ok {
		return t, fmt.Errorf("typecasting matcher iface to http-matcher failed with: %s", err)
	}

	pathMatch := _matcher.Get(common.PathMatch)
	_path := path.Clean("/" + strings.TrimPrefix(r.URL.Path, pathMatch))
	if strings.HasSuffix(r.URL.Path, "/") {
		_path += "/"
	}

	r.URL.Path = _path
	val, err := w.SmartOps()
	if err != nil || val > 0 {
		if err != nil {
			return t, fmt.Errorf("running smart ops failed with: %s", err)
		}
		return t, fmt.Errorf("exited: %d", val)
	}

	err = w.srv.Http().LowLevelAssetHandler(&http.HeadlessAssetsDefinition{
		FileSystem:            w.root,
		SinglePageApplication: true,
		Directory:             "/",
	}, _w, r)

	return time.Now(), err
}

func (w *Website) Match(matcher commonIface.MatchDefinition) (currentMatchIndex matcherSpec.Index) {
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

func (w *Website) Validate(matcher commonIface.MatchDefinition) error {
	if w.Match(matcher) == matcherSpec.NoMatch {
		return errors.New("Website paths or method does not match")
	}

	return nil
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

func (w *Website) Service() commonIface.ServiceComponent {
	return w.srv
}

func (w *Website) Config() *structureSpec.Website {
	return &w.config
}

func (w *Website) Commit() string {
	return w.commit
}

func (w *Website) Matcher() commonIface.MatchDefinition {
	return w.matcher
}

func (w *Website) getFileId() (string, error) {
	assetHash, err := methods.GetTNSAssetPath(w.project, w.config.Id, w.branch)
	if err != nil {
		return "", fmt.Errorf("getting website asset path failed with: %s", err)
	}

	assetHashObject, err := w.srv.Tns().Fetch(assetHash)
	if err != nil {
		return "", fmt.Errorf("fetching asset hash for project: `%s` website: `%s`, branch: `%s` failed with: %w", w.project, w.config.Id, w.branch, err)
	}

	fileId, ok := assetHashObject.Interface().(string)
	if !ok {
		return "", fmt.Errorf("could not resolve asset ID for given website on projectID: `%s`, websiteId `%s`, branch`%s` ", w.project, w.config.Id, w.branch)
	}

	return fileId, nil
}

func (w *Website) getAsset() error {
	fileId, err := w.getFileId()
	if err != nil {
		return fmt.Errorf("getting website asset failed with: %s", err)
	}

	dagReader, err := w.srv.Node().GetFile(w.ctx, fileId)
	if err != nil {
		return fmt.Errorf("getting build zip failed with: %w", err)
	}

	size, _ := readerutil.Size(dagReader)
	if _, err = dagReader.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("seeking start in build zip failed with: %w", err)
	}

	zipReader, err := zip.NewReader(
		readerutil.NewBufferingReaderAt(dagReader),
		size,
	)
	if err != nil {
		return fmt.Errorf("reading build zip failed with: %w", err)
	}

	var computedPaths []string

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

func (w *Website) Id() string {
	return w.config.Id
}

func (w *Website) CachePrefix() string {
	return w.matcher.Host
}
