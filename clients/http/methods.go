package http

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

func (c *Client) do(path, method string, data interface{}, ret interface{}) error {
	req, err := http.NewRequestWithContext(c.ctx, method, c.url+path, nil)
	if err != nil {
		return fmt.Errorf("%s -- `%s` failed with %s", method, path, err.Error())
	}

	req.Header.Add("Authorization", c.auth_header)
	if data != nil {
		_data, err := json.Marshal(data)
		if err != nil {
			return fmt.Errorf("%s -- `%s` failed to marshal data with %s", method, path, err.Error())
		}
		req.Header.Add("Content-Type", "application/json")
		req.Body = io.NopCloser(bytes.NewReader(_data))
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("%s -- `%s` failed with %s", method, path, err.Error())
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s -- `%s` failed with status: %s", method, path, resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("%s -- `%s` failed with %s", method, path, err.Error())
	}

	if ret != nil {
		err = json.Unmarshal(body, ret)
		if err != nil {
			return fmt.Errorf("%s -- `%s` failed to parse json with %s", method, path, err.Error())
		}
	}

	return nil
}

func (c *Client) Get(path string, ret interface{}) error {
	return c.do(path, http.MethodGet, nil, ret)
}

func (c *Client) Post(path string, data interface{}, ret interface{}) error {
	return c.do(path, http.MethodPost, data, ret)
}

func (c *Client) Put(path string, data interface{}, ret interface{}) error {
	return c.do(path, http.MethodPut, data, ret)
}

func (c *Client) Delete(path string, data interface{}, ret interface{}) error {
	return c.do(path, http.MethodDelete, data, ret)
}

func (c *Client) Client() *http.Client {
	return c.client
}

func (c *Client) Context() context.Context {
	return c.ctx
}

func (c *Client) AuthHeader() string {
	return c.auth_header
}

func (c *Client) Url() string {
	return c.url
}

func (c *Client) Provider() string {
	return c.provider
}

func (c *Client) Token() string {
	return c.token
}
