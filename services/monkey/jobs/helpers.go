package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/taubyte/tau/core/services/tns"
	"github.com/taubyte/tau/pkg/git"
	specs "github.com/taubyte/tau/pkg/specs/common"
	"github.com/taubyte/tau/pkg/specs/methods"
	"github.com/taubyte/tau/utils/maps"
)

func (c Context) storeLogFile(file *os.File) (string, error) {
	file.Seek(0, io.SeekStart)
	cid, err := c.Node.AddFile(file)
	if err != nil {
		return "", fmt.Errorf("adding logs to node failed with: %w", err)
	} else {

		if err = c.pushStash(cid); err != nil {
			logger.Error("hoarding log cid `%s` of job `%s` failed with: %s", cid, c.Job.Id, err.Error())
		} else {
			logger.Info("hoarded `%s`", cid)
		}
	}

	return cid, nil
}

// TnsRetryFor is how long a job waits for a TNS key another job publishes.
var TnsRetryFor = 30 * time.Second

// retryTns calls fetch every second until it succeeds or TnsRetryFor passes,
// so a key that lands a second later costs a second, not a fixed long sleep.
func retryTns(ctx context.Context, fetch func() (tns.Object, error)) (tns.Object, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	deadline := time.Now().Add(TnsRetryFor)
	for {
		obj, err := fetch()
		if err == nil || time.Now().After(deadline) {
			return obj, err
		}
		select {
		case <-ctx.Done():
			return nil, err
		case <-time.After(time.Second):
		}
	}
}

func (c Context) fetchConfigSshUrl() (sshString string, err error) {
	tnsPath := specs.NewTnsPath([]string{"resolve", "repo", "github", strconv.Itoa(c.ConfigRepoId)})
	tnsObj, err := retryTns(c.ctx, func() (tns.Object, error) { return c.Tns.Fetch(tnsPath) })
	if err != nil {
		return "", fmt.Errorf("fetching config ssh url failed with: %s", err)
	}

	obj := maps.SafeInterfaceToStringKeys(tnsObj.Interface())
	if ssh, ok := obj["ssh"]; ok {
		if sshString, ok = ssh.(string); ok {
			return
		}
	}

	err = errors.New("ssh key not resolved from configuration repository")

	return
}

func (c Context) getResourceRepositoryId() (id string, err error) {
	gitRepoId := strconv.Itoa(c.Job.Meta.Repository.ID)
	repoPath, err := methods.GetRepositoryPath(strings.ToLower(c.Job.Meta.Repository.Provider), gitRepoId, c.ProjectID)
	if err != nil {
		return
	}

	// The config job publishes this key; when both jobs run at once, the build
	// can get here first.
	resp, err := retryTns(c.ctx, func() (tns.Object, error) {
		resp, err := c.Tns.Fetch(repoPath.AllResources())
		if err == nil {
			if key, ok := resp.Interface().(string); !ok || len(key) < 1 {
				err = fmt.Errorf("could not find git repo id `%s` key in tns", gitRepoId)
			}
		}
		return resp, err
	})
	if err != nil {
		return
	}

	key := resp.Interface().(string)

	keySplit := strings.Split(key, "/")
	id = keySplit[len(keySplit)-1]

	return
}

func (c Context) handleCompressedBuild(id string, rsk io.ReadSeekCloser) error {
	if rsk == nil {
		return nil
	}

	cid, err := c.StashBuildFile(rsk)
	if err != nil {
		return fmt.Errorf("stashing build failed with: %s", err)
	}

	if err = c.publishAsset(id, cid); err == nil && c.reuseKey != "" {
		builtModules.Store(c.reuseKey, cid)
	}
	return err
}

// publishAsset records cid as the build of resource id on the job and in TNS.
func (c Context) publishAsset(id, cid string) error {
	c.Job.SetCid(id, cid)

	assetKey, err := methods.GetTNSAssetPath(c.ProjectID, id, c.Job.Meta.Repository.Branch)
	if err != nil {
		return err
	}

	for i := 0; i < 5; i++ {
		if err = c.Tns.Push(assetKey.Slice(), cid); err == nil {
			break
		}
	}

	return err
}

func (c Context) handleLog() error {
	logCid, err := c.storeLogFile(c.LogFile)
	if err != nil {
		return fmt.Errorf("storing log file for job `%s` failed with: %s", c.Job.Id, err)
	}

	c.Job.SetLog(time.Now().Format(time.RFC3339), logCid)
	return nil
}

func (c *Context) cloneAndSet() error {
	uri := c.Job.Meta.Repository.URI
	if git.IsLocalURI(uri) {
		localPath := strings.TrimPrefix(uri, git.LocalURIScheme)
		c.gitDir = localPath
		c.WorkDir = localPath
		json.NewEncoder(c.LogFile).Encode(struct {
			Op        string `json:"op"`
			Status    string `json:"status"`
			Timestamp int64  `json:"timestamp"`
		}{
			Op:        "git-open",
			Status:    "success",
			Timestamp: time.Now().UnixNano(),
		})
		return nil
	}
	json.NewEncoder(c.LogFile).Encode(struct {
		Op        string `json:"op"`
		Url       string `json:"url"`
		Branch    string `json:"branch"`
		Timestamp int64  `json:"timestamp"`
	}{
		Op:        "git-clone",
		Url:       uri,
		Branch:    c.Job.Meta.Repository.Branch,
		Timestamp: time.Now().UnixNano(),
	})
	repo, err := git.New(
		c.ctx,
		git.URL(c.Job.Meta.Repository.URI),
		git.SSHKey(c.DeployKey),
		git.Temporary(),
		git.Shallow(),
		git.Branch(c.Job.Meta.Repository.Branch),
		git.Output(c.LogFile),
	)
	if err != nil {
		json.NewEncoder(c.LogFile).Encode(struct {
			Op        string `json:"op"`
			Status    string `json:"status"`
			Timestamp int64  `json:"timestamp"`
			Error     string `json:"error"`
		}{
			Op:        "git-clone",
			Status:    "error",
			Error:     err.Error(),
			Timestamp: time.Now().UnixNano(),
		})
		return fmt.Errorf("new git repo failed with: %s", err)
	}

	c.gitDir, c.WorkDir = repo.Root(), repo.Dir()

	json.NewEncoder(c.LogFile).Encode(struct {
		Op        string `json:"op"`
		Status    string `json:"status"`
		Timestamp int64  `json:"timestamp"`
	}{
		Op:        "git-clone",
		Status:    "success",
		Timestamp: time.Now().UnixNano(),
	})

	return nil
}
