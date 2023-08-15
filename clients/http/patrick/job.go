package patrick

import (
	"fmt"
	"io"
	"net/http"

	patrickIface "github.com/taubyte/go-interfaces/services/patrick"
)

type data struct {
	ProjectId string
	JobIds    []string
}

func (c *Client) Jobs(projectId string) (jobList []string, err error) {
	var jobs data
	url := "/jobs/" + projectId
	if err = c.Get(url, &jobs); err != nil {
		err = fmt.Errorf("failed getting jobs for project `%s` with: %w", projectId, err)
		return
	}

	return jobs.JobIds, nil
}

func (c *Client) Job(jid string) (job *patrickIface.Job, err error) {
	receive := &struct {
		Job patrickIface.Job
	}{}
	url := "/job/" + jid
	if err = c.Get(url, &receive); err != nil {
		err = fmt.Errorf("failed getting job `%s` with: %w", jid, err)
		return
	}

	return &receive.Job, nil
}

func (c *Client) LogFile(jobId, resourceId string) (log io.ReadCloser, err error) {
	method := http.MethodGet
	path := "/logs/" + jobId + "/" + resourceId

	req, err := http.NewRequestWithContext(c.Context(), method, c.Url()+path, nil)
	if err != nil {
		err = fmt.Errorf("%s -- `%s` failed with %s", method, path, err.Error())
		return
	}

	req.Header.Add("Authorization", c.AuthHeader())
	resp, err := c.Http().Do(req)
	if err != nil {
		err = fmt.Errorf("%s -- `%s` failed with %s", method, path, err.Error())
		return
	}

	go func() {
		<-c.Context().Done()
		resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		err = fmt.Errorf("%s -- `%s` failed with status: %s", method, path, resp.Status)
		return
	}

	return resp.Body, nil
}

func (c *Client) Cancel(jid string) (response interface{}, err error) {
	url := "/cancel/" + jid
	if err = c.Post(url, nil, &response); err != nil {
		err = fmt.Errorf("failed getting job `%s` with: %w", jid, err)
		return
	}

	return
}

func (c *Client) Retry(jid string) (response interface{}, err error) {
	url := "/retry/" + jid
	if err = c.Post(url, nil, &response); err != nil {
		err = fmt.Errorf("failed getting job `%s` with: %w", jid, err)
		return
	}

	return
}
