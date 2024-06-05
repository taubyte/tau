package http

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
		return fmt.Errorf("%s -- `%s` failed with: %s", method, path, err.Error())
	}

	req.Header.Add("Authorization", c.auth_header)
	if data != nil {
		_data, err := json.Marshal(data)
		if err != nil {
			return fmt.Errorf("%s -- `%s` failed to marshal data with: %s", method, path, err.Error())
		}
		req.Header.Add("Content-Type", "application/json")
		req.Body = io.NopCloser(bytes.NewReader(_data))
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("%s -- `%s` do failed with: %s", method, path, err.Error())
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusBadRequest {
		return fmt.Errorf("%s -- `%s` failed with status: %s", method, path, resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("%s -- `%s` read failed with: %s", method, path, err.Error())
	}

	_err := &errorResponse{}
	if err := json.Unmarshal(body, _err); err != nil || _err.Error != "" {
		if err != nil {
			return fmt.Errorf("%s -- `%s` Unmarshal error failed with: %s", method, path, err.Error())
		}
		return fmt.Errorf("%s -- `%s` failed with: %s", method, path, _err.Error)
	}

	if ret != nil {
		err = json.Unmarshal(body, ret)
		if err != nil {
			return fmt.Errorf("%s -- `%s` failed to parse json with: %s", method, path, err.Error())
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

func (c *Client) delete(path string, data interface{}, ret interface{}) error {
	return c.do(path, http.MethodDelete, data, ret)
}
