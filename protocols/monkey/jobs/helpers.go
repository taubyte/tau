package jobs

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/taubyte/go-interfaces/builders"
	containers "github.com/taubyte/go-simple-container"
	git "github.com/taubyte/go-simple-git"
	specs "github.com/taubyte/go-specs/common"
	"github.com/taubyte/go-specs/methods"
	hoarderClient "github.com/taubyte/odo/protocols/hoarder/api/p2p"
	"github.com/taubyte/utils/maps"
)

type errorMessage string

func (e *errorMessage) append(format string, a ...any) {
	*e += errorMessage(fmt.Sprintf("\n"+format, a))
}
func (e *errorMessage) write(file *os.File) *errorMessage {
	if errS := string(*e); len(errS) > 0 {
		_, err := file.WriteString(errS)
		if err != nil {
			e.append(err.Error())
		}
	}

	return e
}
func (e *errorMessage) Error() error {
	if err := string(*e); len(err) > 0 {
		return errors.New(err)
	}

	return nil
}

func (c Context) storeLogFile(file *os.File) (string, error) {
	errMsg := new(errorMessage)
	_, err := file.Seek(0, io.SeekStart)
	if err != nil {
		errMsg.append(err.Error())
	}

	logCid, err := c.Node.AddFile(file)
	if err != nil {
		errMsg.append("writing cid of job `%s` failed with: %s\n", c.Job.Id, err)
	}

	hoarder, err := hoarderClient.New(c.OdoClientNode.Context(), c.OdoClientNode)
	if err != nil {
		errMsg.append(err.Error())
	}

	// Stash the logs
	_, err = hoarder.Stash(logCid)
	if err != nil {
		errMsg.append("hoarding log cid `%s` of job `%s` failed with: %s", logCid, c.Job.Id, err)
	}

	// Not handling this error due to hoarder failing
	errMsg.write(file)

	return logCid, nil
}

func (c Context) fetchConfigSshUrl() (sshString string, err error) {
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
		return nil, output.Logs().FormatErr("build failed with: %s", err)
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

func (c Context) handleCompressedBuild(id string, rsk io.ReadSeekCloser) error {
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

func (c Context) handleLog(id string, logs *os.File) error {
	logCid, err := c.storeLogFile(logs)
	if err != nil {
		return fmt.Errorf("storing log file for job `%s` failed with: %s", c.Job.Id, err)
	}

	c.Job.SetLog(id, logCid)
	return nil
}

func (c Context) handleBuildDetails(id string, compressedBuild io.ReadSeekCloser, logs *os.File) error {
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
