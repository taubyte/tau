package patrick

import (
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

const streamLogsSilence = 2 * time.Minute

// StreamLogs writes job jid's log to w as the build writes it, and returns
// once the job is done. A job that already finished gets its stored log.
func (c *Client) StreamLogs(jid string, w io.Writer) error {
	url := "ws" + strings.TrimPrefix(c.http.Url(), "http") + "/stream/logs/" + jid

	dialer := *websocket.DefaultDialer
	if t, ok := c.http.Client().Transport.(*http.Transport); ok && t.TLSClientConfig != nil {
		dialer.TLSClientConfig = t.TLSClientConfig.Clone()
	} else {
		dialer.TLSClientConfig = &tls.Config{}
	}

	conn, resp, err := dialer.DialContext(c.http.Context(), url, http.Header{"Authorization": {c.http.AuthHeader()}})
	if err != nil {
		if resp != nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return fmt.Errorf("streaming logs of job `%s` failed with %s: %s", jid, resp.Status, strings.TrimSpace(string(body)))
		}
		return fmt.Errorf("streaming logs of job `%s` failed with: %w", jid, err)
	}
	defer conn.Close()

	finished := make(chan struct{})
	defer close(finished)
	go func() {
		select {
		case <-c.http.Context().Done():
			conn.Close()
		case <-finished:
		}
	}()

	// Patrick pings while a job is quiet; silence means the connection is dead
	conn.SetPingHandler(func(data string) error {
		conn.SetReadDeadline(time.Now().Add(streamLogsSilence))
		return conn.WriteControl(websocket.PongMessage, []byte(data), time.Now().Add(10*time.Second))
	})

	for {
		conn.SetReadDeadline(time.Now().Add(streamLogsSilence))
		_, data, err := conn.ReadMessage()
		if err != nil {
			var ce *websocket.CloseError
			if errors.As(err, &ce) {
				if ce.Code == websocket.CloseNormalClosure {
					return nil
				}
				return fmt.Errorf("streaming logs of job `%s` failed with: %s", jid, ce.Text)
			}
			return fmt.Errorf("streaming logs of job `%s` failed with: %w", jid, err)
		}
		if _, err = w.Write(data); err != nil {
			return err
		}
	}
}
