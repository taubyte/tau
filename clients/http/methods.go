package http // This code is related to HTTP operations

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// The do function is responsible for making the HTTP request.
// It returns an error if there's an issue creating the request.
func (c *Client) do(path, method string, data interface{}, ret interface{}) error {
	req, err := http.NewRequestWithContext(c.ctx, method, c.url+path, nil)
	if err != nil {
		return fmt.Errorf("%s -- `%s` failed with %s", method, path, err.Error())
	}
	// The code adds an Authorization header to the request using the c.auth_header field.
	// If there is data to be sent in the request body, it is marshaled to JSON using json.Marshal and set as the request body.
	// The Content-Type header is set to application/json to indicate that the request body is in JSON format.
	req.Header.Add("Authorization", c.auth_header)
	if data != nil {
		_data, err := json.Marshal(data)
		if err != nil {
			return fmt.Errorf("%s -- `%s` failed to marshal data with %s", method, path, err.Error())
		}
		req.Header.Add("Content-Type", "application/json")
		req.Body = io.NopCloser(bytes.NewReader(_data))
	}

	// The code sends the HTTP request using c.client.Do(req) and captures the response.	
	resp, err := c.client.Do(req)
	if err != nil {   // It returns an error if there's an issue making the request.
		return fmt.Errorf("%s -- `%s` failed with %s", method, path, err.Error())
	}
	// The defer resp.Body.Close() statement ensures that the response body is closed after the function returns.
	defer resp.Body.Close()  

	// Check if the response status code is not http.StatusOK (200). If it's not, it returns an error.
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s -- `%s` failed with status: %s", method, path, resp.Status)
	}

	// Reads the response body using io.ReadAll and returns an error if there's an issue reading the body.
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("%s -- `%s` failed with %s", method, path, err.Error())
	}

	// If the ret parameter is not nil, it attempts to parse the response body as JSON using json.Unmarshal.
	if ret != nil {
		err = json.Unmarshal(body, ret)
		if err != nil {  // It returns an error if there's an issue parsing the JSON.
			return fmt.Errorf("%s -- `%s` failed to parse json with %s", method, path, err.Error())
		}
	}

	return nil // If everything is successful, the function returns nil to indicate no error.
}
// Get, Post, Put, Delete provide a convenient way to make HTTP requests with different methods
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
