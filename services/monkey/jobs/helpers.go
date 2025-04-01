package jobs

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ipfs/go-log/v2"
	"github.com/taubyte/tau/core/builders"
	"github.com/taubyte/tau/pkg/git"
	specs "github.com/taubyte/tau/pkg/specs/common"
	"github.com/taubyte/tau/pkg/specs/methods"
	chidori "github.com/taubyte/utils/logger/zap"
	"github.com/taubyte/utils/maps"
)

func (c Context) storeLogFile(file *os.File) (string, error) {
	file.Seek(0, io.SeekStart)
	cid, err := c.Node.AddFile(file)
	if err != nil {
		return "", fmt.Errorf("adding logs to node failed with: %w", err)
	} else {

		if _, err = c.Monkey.Hoarder().Stash(cid); err != nil {
			chidori.Format(logger, log.LevelError, "hoarding log cid `%s` of job `%s` failed with: %s", cid, c.Job.Id, err.Error())
		} else {
			chidori.Format(logger, log.LevelInfo, "hoarded `%s`", cid)
		}
	}

	return cid, nil
}

func (c Context) fetchConfigSshUrl() (sshString string, err error) {
	tnsPath := specs.NewTnsPath([]string{"resolve", "repo", "github", strconv.Itoa(c.ConfigRepoId)})
	tnsObj, err := c.Tns.Fetch(tnsPath)
	// TODO: This should return
	if err != nil {
		time.Sleep(30 * time.Second)
		tnsObj, err = c.Tns.Fetch(tnsPath)
		if err != nil {
			err = fmt.Errorf("fetching config ssh url failed with: %s", err)
		}
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

// for singular resource repositories(not code repo), error should be nil, the monkey will be handling this logic
func (c Context) mergeBuildLogs(logs builders.Logs) {
	if logs == nil {
		return
	}

	if _, err := logs.CopyTo(c.LogFile); err != nil {
		c.LogFile.WriteString("\nMonkey Error:\n" + err.Error())
	}
}

func (c Context) getResourceRepositoryId() (id string, err error) {
	gitRepoId := strconv.Itoa(c.Job.Meta.Repository.ID)
	repoPath, err := methods.GetRepositoryPath(strings.ToLower(c.Job.Meta.Repository.Provider), gitRepoId, c.ProjectID)
	if err != nil {
		return
	}

	resp, err := c.Tns.Fetch(repoPath.AllResources())
	if err != nil {
		return
	}

	key, ok := resp.Interface().(string)
	if !ok || len(key) < 1 {
		err = fmt.Errorf("could not find git repo id `%s` key in tns", gitRepoId)
	}

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

func (c Context) handleLog(id string) error {
	logCid, err := c.storeLogFile(c.LogFile)
	if err != nil {
		return fmt.Errorf("storing log file for job `%s` failed with: %s", c.Job.Id, err)
	}

	c.Job.SetLog(id, logCid)
	return nil
}

func (c *Context) cloneAndSet() error {
	repo, err := git.New(
		c.ctx,
		git.URL(c.Job.Meta.Repository.SSHURL),
		git.SSHKey(c.DeployKey),
		git.Temporary(),
		git.Branch(c.Job.Meta.Repository.Branch),
	)
	if err != nil {
		return fmt.Errorf("new git repo failed with: %s", err)
	}

	c.gitDir, c.WorkDir = repo.Root(), repo.Dir()
	return nil
}
