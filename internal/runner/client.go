package runner

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

type client struct {
	base  string
	token string
	http  *http.Client
}

// newTransport is the agent's HTTP transport: HTTP/1.1 only, a few
// connections, none kept for long. Over HTTP/2 every request shares one
// connection, and one that died silently (a router, a NAT, a network change)
// is used again and again until the kernel gives up on it — about a quarter
// of an hour in which every heartbeat times out and the server takes the
// machine for gone. Over HTTP/1.1 a request that fails takes only its own
// connection with it, and the next one dials afresh.
func newTransport() *http.Transport {
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 15 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
		IdleConnTimeout:       60 * time.Second,
		MaxIdleConnsPerHost:   4,
		// A non-nil, empty map turns HTTP/2 off (see net/http's Transport docs).
		TLSNextProto: map[string]func(string, *tls.Conn) http.RoundTripper{},
	}
}

func newClient(base, token string) *client {
	// Longer than the server's 25 s claim long-poll.
	return &client{base: base, token: token, http: &http.Client{Timeout: 45 * time.Second, Transport: newTransport()}}
}

// transportFailed: the request did not get an HTTP answer (a timeout, a
// reset, no route): whatever connections are idle may be dead too.
func transportFailed(err error) bool {
	var ae *apiError
	return err != nil && !errors.As(err, &ae) && !errors.Is(err, context.Canceled)
}

// post sends a JSON body and decodes a JSON answer into out (when non-nil
// and the server returned one). A 204 leaves out untouched and returns
// (false, nil).
func (c *client) post(ctx context.Context, path string, body, out any) (bool, error) {
	var buf bytes.Buffer
	if body == nil {
		body = struct{}{}
	}
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		return false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, &buf)
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("User-Agent", "forge-agent/"+Version)
	resp, err := c.http.Do(req)
	if err != nil {
		if transportFailed(err) {
			c.http.CloseIdleConnections() // do not hand the next request a connection that may be just as dead
		}
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent {
		return false, nil
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if resp.StatusCode >= 300 {
		return false, &apiError{status: resp.StatusCode, body: string(bytes.TrimSpace(raw))}
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return false, fmt.Errorf("decoding %s: %w", path, err)
		}
	}
	return true, nil
}

type apiError struct {
	status int
	body   string
}

func (e *apiError) Error() string { return fmt.Sprintf("HTTP %d: %s", e.status, e.body) }
