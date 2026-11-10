package runtime

import (
	"fmt"
	"sync"

	"github.com/taubyte/tau/pkg/config"
	"github.com/taubyte/tau/pkg/platformerr"
)

// projectLoad is what a project's live instances hold on this node.
// shortcut: one registry per process, keyed by project id; fine as long as a process runs one substrate.
var projectLoad = struct {
	mu sync.Mutex
	m  map[string]*load
}{m: map[string]*load{}}

type load struct {
	instances int
	bytes     uint64
}

// acquire reserves one instance of mem bytes for project, or says the project is over its caps. Zero caps are unlimited.
func acquire(project string, mem uint64, caps config.Caps) error {
	if caps.FunctionMemory != 0 && mem > caps.FunctionMemory {
		return platformerr.New(platformerr.OverQuota, fmt.Errorf("function memory %d bytes is over the project limit of %d bytes", mem, caps.FunctionMemory))
	}
	projectLoad.mu.Lock()
	defer projectLoad.mu.Unlock()
	l := projectLoad.m[project]
	if l == nil {
		l = &load{}
		projectLoad.m[project] = l
	}
	if caps.Instances != 0 && l.instances >= caps.Instances {
		return platformerr.New(platformerr.OverQuota, fmt.Errorf("project is running its limit of %d instances", caps.Instances))
	}
	l.instances++
	l.bytes += mem
	return nil
}

func release(project string, mem uint64) {
	projectLoad.mu.Lock()
	defer projectLoad.mu.Unlock()
	if l := projectLoad.m[project]; l != nil {
		l.instances--
		l.bytes -= mem
		if l.instances <= 0 {
			delete(projectLoad.m, project)
		}
	}
}
