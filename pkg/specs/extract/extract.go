package extract

import (
	"errors"
	"path"
	"regexp"
	"strings"

	"github.com/taubyte/tau/pkg/specs/common"
)

func Tns() *tnsHelper {
	return &tnsHelper{}
}

func (ep *Path) Branch() string {
	return ep.branch
}

func (ep *Path) Commit() string {
	return ep.commitId
}

func (ep *Path) ResourceType() string {
	return ep.resourceType
}

func (ep *Path) Resource() string {
	return ep.resourceId
}

func (ep *Path) Application() string {
	return ep.appId
}

func (ep *Path) Project() string {
	return ep.projectId
}

func init() {
	// check https://regex101.com/r/fu1CSU/1
	matcher = regexp.MustCompile(`branches/(?P<branch>[^/]+)/?(commit/(?P<commitId>[^/]+)/)?projects/(?P<projectId>[^/]+)/?((applications/(?P<appId>[^/]+)/?)?((?P<resourceType>[^/]+)/(?P<resourceId>[^/]+))?)?`)

	if matcher.SubexpIndex("projectId") < 0 || matcher.SubexpIndex("appId") < 0 || matcher.SubexpIndex("resourceType") < 0 || matcher.SubexpIndex("resourceId") < 0 || matcher.SubexpIndex("branch") < 0 || matcher.SubexpIndex("commitId") < 0 {
		panic("go-spec path parser regex has an issue")
	}
}

func (ep *Path) Parse(path *common.TnsPath) *Path {
	m := matcher.FindStringSubmatch(path.String())
	if len(m) == 0 { // probably should handle error instead
		return ep
	}

	ep.projectId = m[matcher.SubexpIndex("projectId")]
	ep.appId = m[matcher.SubexpIndex("appId")]
	ep.resourceType = m[matcher.SubexpIndex("resourceType")]
	ep.resourceId = m[matcher.SubexpIndex("resourceId")]
	ep.commitId = m[matcher.SubexpIndex("commitId")]
	ep.branch = m[matcher.SubexpIndex("branch")]

	return ep
}

func (tns *tnsHelper) BasicPath(_path string) (*Path, error) {
	if len(_path) == 0 {
		return nil, errors.New("extraction path cannot be empty")
	}

	cleanPath := path.Join("/" + _path)

	// Here we are splitting on / and removing the first so that the first item is not empty.
	return (&Path{}).Parse(common.NewTnsPath(strings.Split(cleanPath, "/")[1:])), nil
}
