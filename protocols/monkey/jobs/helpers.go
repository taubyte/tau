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
	"github.com/taubyte/go-interfaces/builders"
	containers "github.com/taubyte/go-simple-container"
	git "github.com/taubyte/go-simple-git"
	specs "github.com/taubyte/go-specs/common"
	"github.com/taubyte/go-specs/methods"
	hoarderClient "github.com/taubyte/tau/clients/p2p/hoarder"
	chidori "github.com/taubyte/utils/logger/zap"
	"github.com/taubyte/utils/maps"
)

func (c *Context) storeLogFile(file *os.File) (string, error) {
	if len(c.debug) > 0 {
		if _, err := file.Seek(0, io.SeekEnd); err == nil {
			file.WriteString("DEBUG: \n" + c.debug + "\n")
		}
	}

	file.Seek(0, io.SeekStart)
	cid, err := c.Node.AddFile(file)
	if err != nil {
		return "", fmt.Errorf("adding logs to node failed with: %s", err.Error())
	} else {
		hoarder, err := hoarderClient.New(c.OdoClientNode.Context(), c.OdoClientNode)
		if err != nil {
			chidori.Format(logger, log.LevelError, "new hoarder client failed with: %s", err)
		}

		if _, err = hoarder.Stash(cid); err != nil {
			chidori.Format(logger, log.LevelError, "hoarding log cid `%s` of job `%s` failed with: %s", cid, c.Job.Id, err.Error())
		}
	}

	return cid, nil
}

func (c *Context) fetchConfigSshUrl() (sshString string, err error) {
	tnsPath := specs.NewTnsPath([]string{"resolve", "repo", "github", strconv.Itoa(c.ConfigRepoId)})
	tnsObj, err := c.Tns.Fetch(tnsPath)
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

func closeReader(reader io.ReadCloser) {
	if reader != nil {
		reader.Close()
	}
}

func buildAndSetLog(builder builders.Builder, logs *builders.Logs, ops ...containers.ContainerOption) (builders.Output, error) {
	output, err := builder.Build(ops...)
	defer func() {
		if output != nil {
			*logs = output.Logs()
		}
	}()
	if err != nil {
		return nil, err
	}

	return output, nil
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

func (c *Context) handleCompressedBuild(id string, rsk io.ReadSeekCloser) error {
	cid, err := c.StashBuildFile(rsk)
	if err != nil {
		return fmt.Errorf("stashing build failed with: %s", err)
	}

	c.Job.SetCid(id, cid)

	assetKey, err := methods.GetTNSAssetPath(c.ProjectID, id, specs.DefaultBranch)
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

func (c *Context) handleLog(id string, logs *os.File) error {
	logCid, err := c.storeLogFile(logs)
	if err != nil {
		return fmt.Errorf("storing log file for job `%s` failed with: %s", c.Job.Id, err)
	}

	c.Job.SetLog(id, logCid)
	return nil
}

func (c *Context) handleBuildDetails(id string, compressedBuild io.ReadSeekCloser, logs *os.File) error {
	if logs != nil {
		if err := c.handleLog(id, logs); err != nil {
			return err
		}
	}

	if compressedBuild != nil {
		if err := c.handleCompressedBuild(id, compressedBuild); err != nil {
			return err
		}
	}

	return nil
}

func (c *Context) cloneAndSet() error {
	repo, err := git.New(
		c.ctx,
		git.URL(c.Job.Meta.Repository.SSHURL),
		git.SSHKey(c.DeployKey),
		git.Temporary(),
		git.Branch(c.Job.Meta.Repository.Branch),
		// uncomment to keep directory
		// git.Preserve(),
	)
	if err != nil {
		return fmt.Errorf("new git repo failed with: %s", err)
	}

	c.gitDir, c.WorkDir = repo.Root(), repo.Dir()
	return nil
}

func (c *Context) addDebugMsg(level log.LogLevel, format string, args ...any) {
	msg := chidori.Format(logger, level, format, args...)
	c.debug += msg + "\n"
}
