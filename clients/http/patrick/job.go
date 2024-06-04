package patrick

import (
	"fmt"
	"io"
	"net/http"

	patrickIface "github.com/taubyte/tau/core/services/patrick"
)

type data struct {
	ProjectId string
	JobIds    []string
}

func (c *Client) Jobs(projectId string) (jobList []string, err error) {
	var jobs data
	url := "/jobs/" + projectId
	if err = c.http.Get(url, &jobs); err != nil {
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
	if err = c.http.Get(url, &receive); err != nil {
		err = fmt.Errorf("failed getting job `%s` with: %w", jid, err)
		return
	}

	return &receive.Job, nil
}

func (c *Client) LogFile(jobId, resourceId string) (log io.ReadCloser, err error) {
	method := http.MethodGet
	path := "/logs" + "/" + resourceId

	req, err := http.NewRequestWithContext(c.http.Context(), method, c.http.Url()+path, nil)
	if err != nil {
		err = fmt.Errorf("%s -- `%s` failed with %s", method, path, err.Error())
		return
	}

	req.Header.Add("Authorization", c.http.AuthHeader())
	resp, err := c.http.Client().Do(req)
	if err != nil {
		err = fmt.Errorf("%s -- `%s` failed with %s", method, path, err.Error())
		return
	}

	go func() {
		<-c.http.Context().Done()
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
	if err = c.http.Post(url, nil, &response); err != nil {
		err = fmt.Errorf("failed getting job `%s` with: %w", jid, err)
		return
	}

	return
}

func (c *Client) Retry(jid string) (response interface{}, err error) {
	url := "/retry/" + jid
	if err = c.http.Post(url, nil, &response); err != nil {
		err = fmt.Errorf("failed getting job `%s` with: %w", jid, err)
		return
	}

	return
}
