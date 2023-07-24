package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type errorResponse struct {
	Error string `json:"error"`
}

func (c *Client) do(path, method string, data interface{}, ret interface{}) error {
	req, err := http.NewRequestWithContext(c.ctx, method, c.url+path, nil)
	if err != nil {
		return fmt.Errorf("%s -- `%s` failed with %s", method, path, err)
	}

	req.Header.Add("Authorization", c.auth_header)
	if data != nil {
		_data, err := json.Marshal(data)
		if err != nil {
			return fmt.Errorf("%s -- `%s` failed to marshal data with %s", method, path, err)
		}
		req.Header.Add("Content-Type", "application/json")
		req.Body = io.NopCloser(bytes.NewReader(_data))
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("%s -- `%s` failed with %s", method, path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s -- `%s` failed with status: %s", method, path, resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("%s -- `%s` failed with: %s", method, path, err)
	}

	_err := &errorResponse{}
	if json.Unmarshal(body, _err) != nil || _err.Error != "" {
		return fmt.Errorf("%s -- `%s` failed with: %s", method, path, _err.Error)
	}
	if _err.Error != "" {
		return fmt.Errorf("%s -- `%s` failed with: %s", method, path, _err.Error)
	}

	// Un-marshall to if there is an expected response
	if ret != nil {
		err = json.Unmarshal(body, ret)
		if err != nil {
			return fmt.Errorf("%s -- `%s` parsing body to json failed with: %s", method, path, err)
		}
	}

	return nil
}

func (c *Client) get(path string, ret interface{}) error {
	return c.do(path, http.MethodGet, nil, ret)
}

func (c *Client) post(path string, data interface{}, ret interface{}) error {
	return c.do(path, http.MethodPost, data, ret)
}

func (c *Client) put(path string, data interface{}, ret interface{}) error {
	return c.do(path, http.MethodPut, data, ret)
}

func (c *Client) delete(path string, data interface{}, ret interface{}) error {
	return c.do(path, http.MethodDelete, data, ret)
}
