//go:build ping

package main

//lint:file-ignore U1000 compiled file

import (
	"fmt"
	"io"
	"strings"

	"github.com/taubyte/go-sdk/event"
	"github.com/taubyte/go-sdk/http/client"
)

//export ping
func ping(e event.Event) uint32 {
	if err := runTestEvent(e); err != nil {
		panic(fmt.Sprintf("runTestClient1 failed with %v", err))
	}

	if err := runTestClient1(); err != nil {
		panic(fmt.Sprintf("runTestClient1 failed with %v", err))
	}

	if err := runTestClient2(); err != nil {
		panic(fmt.Sprintf("runTestClient2 failed with %v", err))
	}

	if err := runTestHeaders(); err != nil {
		panic(fmt.Sprintf("runTestHeaders failed with %v", err))
	}

	if err := runTestBody(); err != nil {
		panic(fmt.Sprintf("runTestBody failed with %v", err))
	}

	if err := runTestBody2(); err != nil {
		panic(fmt.Sprintf("runTestBody2 failed with %v", err))
	}

	return 0
}

func runTestBody() error {
	c, err := client.New()
	if err != nil {
		return fmt.Errorf("create client failed with: %v", err)
	}

	req, err := c.Request("http://localhost:9090/here", client.Method("POST"), client.Body([]byte("the required body")))
	if err != nil {
		return fmt.Errorf("create request failed with: %v", err)
	}

	resp, err := req.Do()
	if err != nil {
		return fmt.Errorf("do request failed with: %v", err)
	}
	defer resp.Body().Close()

	_body, err := io.ReadAll(resp.Body())
	if err != nil {
		return fmt.Errorf("read body failed with: %v", err)
	}

	expectedInBody := "yeah that's it"
	bodyContains := strings.Contains(string(_body), expectedInBody)
	if !bodyContains {
		return fmt.Errorf("expected %s to be in body got:\nBODY\n%s\nBODY", expectedInBody, string(_body))
	}

	return err
}

func runTestBody2() error {
	c, err := client.New()
	if err != nil {
		return fmt.Errorf("create client failed with: %v", err)
	}

	req, err := c.Request("http://localhost:9090/here", client.Method("POST"))
	if err != nil {
		return fmt.Errorf("create request failed with: %v", err)
	}

	err = req.Body().Set([]byte("the required body"))
	if err != nil {
		return fmt.Errorf("set body failed with: %v", err)
	}

	resp, err := req.Do()
	if err != nil {
		return fmt.Errorf("do request failed with: %v", err)
	}
	defer resp.Body().Close()

	_body, err := io.ReadAll(resp.Body())
	if err != nil {
		return fmt.Errorf("read body failed with: %v", err)
	}

	expectedInBody := "yeah that's it"
	bodyContains := strings.Contains(string(_body), expectedInBody)
	if !bodyContains {
		return fmt.Errorf("expected %s to be in body got:\nBODY\n%s\nBODY", expectedInBody, string(_body))
	}

	return err
}

func runTestHeaders() error {
	c, err := client.New()
	if err != nil {
		return fmt.Errorf("create client failed with: %v", err)
	}

	headers := map[string][]string{
		"oops": {"hello"},
	}

	// python3 -m http.server 9090
	req, err := c.Request("http://localhost:9090/", client.Method("GET"), client.Headers(headers))
	if err != nil {
		return fmt.Errorf("create request failed with: %v", err)
	}
	// var key, value string
	err = req.Headers().Set("Nice", "yes")
	if err != nil {
		return fmt.Errorf("FAILED SET Headers %v", err)
	}

	_headers, err := req.Headers().GetAll()
	if err != nil {
		return fmt.Errorf("WHAT %v", err)
	}

	fmt.Println("GOT HEADERS")
	for key, value := range _headers {
		fmt.Println(key+": ", value)
	}

	resp, err := req.Do()
	if err != nil {
		return fmt.Errorf("do request failed with: %v", err)
	}
	defer resp.Body().Close()

	return err
}

func runTestEvent(e event.Event) error {
	h, _ := e.HTTP()

	h.Headers().Set("Test", "Test Header")
	_body, err := io.ReadAll(h.Body())
	if err != nil {
		return fmt.Errorf("read body failed with: %s", err.Error())
	}
	host, _ := h.Host()
	path, _ := h.Path()
	userAgent, _ := h.UserAgent()
	acceptEncoding, _ := h.Headers().Get("Accept-Encoding")

	// TODO: test, currently only displaying
	// fmt.Println("Type -> ", e.Type())
	// method, _ := h.Method()
	// fmt.Println("Method --> ", method)
	// headers, _ := h.Headers().List()  # Fixme,  weird stuff
	// queries, _ := h.Query().List()
	query, _ := h.Query().Get("name")

	toWrite := []byte(fmt.Sprintf(`{"ping": "pong","body": "%v","host": "%s","path": "%s","useragent": "%s","acceptencoding": "%s","query": "%s"}`, len(_body), host, path, userAgent, acceptEncoding, query))
	n, err0 := h.Write(toWrite)
	if err0 != nil {
		panic(err0)
	}

	if n != len(toWrite) {
		panic(fmt.Sprintf("Expected to write %d, wrote %d", len(toWrite), n))
	}

	err0 = h.Body().Close()
	if err0 != nil {
		panic(err0)
	}

	h.Return(404)

	return nil
}

func runTestClient1() error {
	c, err := client.New()
	if err != nil {
		return fmt.Errorf("create client failed with: %v", err)
	}

	// python3 -m http.server 9090
	req, err := c.Request("http://localhost:9090/README.md", client.Method("GET"))
	if err != nil {
		return fmt.Errorf("create request failed with: %v", err)
	}

	// req.Method().Set("POST")
	resp, err := req.Do()
	if err != nil {
		return fmt.Errorf("do request failed with: %v", err)
	}
	defer resp.Body().Close()

	_body, err := io.ReadAll(resp.Body())
	if err != nil {
		return fmt.Errorf("read body failed with: %v", err)
	}

	expectedInBody := "this is some stuff in the readme I guess"
	bodyContains := strings.Contains(string(_body), expectedInBody)
	if !bodyContains {
		return fmt.Errorf("expected %s to be in body got:\nBODY\n%s\nBODY", expectedInBody, string(_body))
	}

	return nil
}

func runTestClient2() error {
	c, err := client.New()
	if err != nil {
		return fmt.Errorf("create client failed with: %v", err)
	}

	req, err := c.Request("http://localhost:9090/header", client.Method("POST"))
	if err != nil {
		return fmt.Errorf("create request failed with: %v", err)
	}

	resp, err := req.Do()
	if err != nil {
		return fmt.Errorf("do request failed with: %v", err)
	}
	defer resp.Body().Close()

	_body, err := io.ReadAll(resp.Body())
	if err != nil {
		return fmt.Errorf("read body failed with: %v", err)
	}

	expectedInBody := "You sent a what? :POST"
	bodyContains := strings.Contains(string(_body), expectedInBody)
	if !bodyContains {
		return fmt.Errorf("expected %s to be in body got:\nBODY\n%s\nBODY", expectedInBody, string(_body))
	}

	err = req.Method().Set("DELETE")
	if err != nil {
		return fmt.Errorf("set method failed with: %v", err)
	}

	resp, err = req.Do()
	if err != nil {
		return fmt.Errorf("do request failed with: %v", err)
	}
	defer resp.Body().Close()

	_body, err = io.ReadAll(resp.Body())
	if err != nil {
		return fmt.Errorf("read body failed with: %v", err)
	}

	expectedInBody = "You sent a what? :DELETE"
	bodyContains = strings.Contains(string(_body), expectedInBody)
	if !bodyContains {
		return fmt.Errorf("expected %s to be in body got:\nBODY\n%s\nBODY", expectedInBody, string(_body))
	}

	return nil
}
