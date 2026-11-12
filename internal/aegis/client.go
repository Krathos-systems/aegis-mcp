// Package aegis is the only place that talks HTTP to the Aegis server.
//
// Every admin route sits under /api on the server's public origin, and every
// request carries the admin token as a Bearer credential. The token is never
// logged and never included in an error.
package aegis

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Timeout is how long one request may take unless it sets its own.
const Timeout = 30 * time.Second

// LongTimeout is for requests that make Aegis read a git repository, which it
// allows up to two minutes.
const LongTimeout = 150 * time.Second

// maxBody caps how much of a response is read. Managed files are capped at
// 1 MiB by the server, so this leaves room for JSON listings around them.
const maxBody = 4 << 20

// Client talks to one Aegis server origin with one admin token.
type Client struct {
	baseURL   string
	token     string
	userAgent string
	http      *http.Client
}

// New returns a Client for the given server origin (e.g. https://acme.example.com).
func New(baseURL, token, userAgent string) *Client {
	return &Client{
		baseURL:   strings.TrimRight(baseURL, "/"),
		token:     token,
		userAgent: userAgent,
		http: &http.Client{
			// Each request carries its own deadline (Request.Timeout).
			// Never follow a redirect: it would carry the token to wherever it points.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

// BaseURL is the server origin this client talks to.
func (c *Client) BaseURL() string { return c.baseURL }

// Response is a completed request: status, body and the ETag header if any.
type Response struct {
	Status int
	Body   []byte
	ETag   string
}

// StatusError is a response outside 2xx. Message is the server's own
// sentence (the "error" field of its JSON body) when it sent one.
type StatusError struct {
	Method  string
	Path    string
	Status  int
	Message string
	// Body is the parsed JSON body, kept so callers can surface fields such as
	// "required"/"held" on a 403 or "limit" on a 402.
	Body map[string]any
}

func (e *StatusError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = http.StatusText(e.Status)
	}
	return fmt.Sprintf("%s %s: %d %s", e.Method, e.Path, e.Status, msg)
}

// Request describes one call. Path is relative to /api and must start with "/".
type Request struct {
	Method      string
	Path        string
	Query       url.Values
	Body        []byte
	ContentType string
	IfMatch     string
	// Timeout overrides the default Timeout for this request.
	Timeout time.Duration
}

// Do performs one request and returns an error for any non-2xx status.
func (c *Client) Do(ctx context.Context, r Request) (*Response, error) {
	timeout := r.Timeout
	if timeout == 0 {
		timeout = Timeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	u := c.baseURL + "/api" + r.Path
	if len(r.Query) > 0 {
		u += "?" + r.Query.Encode()
	}
	var body io.Reader
	if r.Body != nil {
		body = bytes.NewReader(r.Body)
	}
	req, err := http.NewRequestWithContext(ctx, r.Method, u, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json, text/markdown;q=0.9, */*;q=0.1")
	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}
	if r.ContentType != "" {
		req.Header.Set("Content-Type", r.ContentType)
	}
	if r.IfMatch != "" {
		req.Header.Set("If-Match", r.IfMatch)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, scrub(err, c.baseURL)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, newStatusError(r.Method, r.Path, resp.StatusCode, data)
	}
	return &Response{Status: resp.StatusCode, Body: data, ETag: resp.Header.Get("ETag")}, nil
}

// SendJSON encodes body as the request's JSON body, then works like JSON.
func (c *Client) SendJSON(ctx context.Context, r Request, body, out any) (*Response, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	r.Body, r.ContentType = data, "application/json"
	return c.JSON(ctx, r, out)
}

// JSON performs a request and decodes a JSON response into out.
func (c *Client) JSON(ctx context.Context, r Request, out any) (*Response, error) {
	resp, err := c.Do(ctx, r)
	if err != nil {
		return nil, err
	}
	if out != nil && len(resp.Body) > 0 {
		if err := json.Unmarshal(resp.Body, out); err != nil {
			return nil, fmt.Errorf("%s %s: response is not JSON: %w", r.Method, r.Path, err)
		}
	}
	return resp, nil
}

// Health performs GET /health (outside /api, no token needed).
func (c *Client) Health(ctx context.Context) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/health", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, scrub(err, c.baseURL)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if resp.StatusCode != http.StatusOK {
		return nil, newStatusError(http.MethodGet, "/health", resp.StatusCode, data)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("GET /health: the server did not answer like Aegis does")
	}
	return out, nil
}

func newStatusError(method, path string, status int, data []byte) *StatusError {
	e := &StatusError{Method: method, Path: path, Status: status}
	var parsed map[string]any
	if json.Unmarshal(data, &parsed) == nil {
		e.Body = parsed
		if msg, ok := parsed["error"].(string); ok {
			e.Message = msg
		}
	}
	return e
}

// scrub keeps transport errors readable without repeating the full URL, which
// may carry query parameters.
func scrub(err error, base string) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return fmt.Errorf("%s %s: %w", ue.Op, base, ue.Err)
	}
	return err
}
