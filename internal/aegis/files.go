package aegis

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Plane is one of the two managed trees: content (Markdown instructions,
// skills, agents) or config (settings.json, mcp.json).
type Plane string

const (
	Content Plane = "content"
	Config  Plane = "config"
)

// ParsePlane accepts "content" or "config".
func ParsePlane(s string) (Plane, error) {
	switch Plane(s) {
	case Content, Config:
		return Plane(s), nil
	}
	return "", fmt.Errorf("plane must be %q or %q, got %q", Content, Config, s)
}

// ErrExists is returned when a create targets a file that is already there.
var ErrExists = errors.New("file already exists")

// CleanPath validates a path inside a managed tree and returns it with each
// segment URL-escaped. The server checks containment too; this catches the
// mistakes early and keeps characters like "?" or "#" out of the URL.
func CleanPath(rel string) (string, error) {
	if rel == "" {
		return "", errors.New("path is empty")
	}
	if strings.HasPrefix(rel, "/") || strings.Contains(rel, `\`) {
		return "", fmt.Errorf("path %q must be relative and use forward slashes", rel)
	}
	parts := strings.Split(rel, "/")
	for i, p := range parts {
		if p == "" || p == "." || p == ".." {
			return "", fmt.Errorf("path %q has an empty, '.' or '..' segment", rel)
		}
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/"), nil
}

// ListFiles returns every file path in a plane that the token may read.
func (c *Client) ListFiles(ctx context.Context, plane Plane) ([]string, error) {
	var out []string
	_, err := c.JSON(ctx, Request{Method: http.MethodGet, Path: "/" + string(plane) + "/files"}, &out)
	return out, err
}

// File is one managed file and the ETag to send back when changing it.
type File struct {
	Body string
	ETag string
}

// ReadFile returns a file's raw body (variables unrendered) and its ETag.
func (c *Client) ReadFile(ctx context.Context, plane Plane, rel string) (*File, error) {
	p, err := CleanPath(rel)
	if err != nil {
		return nil, err
	}
	resp, err := c.Do(ctx, Request{Method: http.MethodGet, Path: "/" + string(plane) + "/files/" + p})
	if err != nil {
		return nil, err
	}
	return &File{Body: string(resp.Body), ETag: resp.ETag}, nil
}

// WriteResult reports what a write did and the file's new ETag.
type WriteResult struct {
	Created bool
	ETag    string
}

// CreateFile creates a file that must not exist yet.
func (c *Client) CreateFile(ctx context.Context, plane Plane, rel, body string) (*WriteResult, error) {
	p, err := CleanPath(rel)
	if err != nil {
		return nil, err
	}
	path := "/" + string(plane) + "/files/" + p
	switch plane {
	case Content:
		// POST refuses an existing file with 409 on the server.
		resp, err := c.Do(ctx, Request{
			Method: http.MethodPost, Path: path,
			Body: []byte(body), ContentType: contentType(rel),
		})
		if err != nil {
			var se *StatusError
			if errors.As(err, &se) && se.Status == http.StatusConflict {
				return nil, fmt.Errorf("%s/%s: %w", plane, rel, ErrExists)
			}
			return nil, err
		}
		return &WriteResult{Created: true, ETag: resp.ETag}, nil
	default:
		// The config plane only has PUT, which creates or overwrites. Check
		// first so a create never silently replaces a file.
		if _, err := c.ReadFile(ctx, plane, rel); err == nil {
			return nil, fmt.Errorf("%s/%s: %w", plane, rel, ErrExists)
		} else if !isStatus(err, http.StatusNotFound) {
			return nil, err
		}
		resp, err := c.Do(ctx, Request{
			Method: http.MethodPut, Path: path,
			Body: []byte(body), ContentType: contentType(rel),
		})
		if err != nil {
			return nil, err
		}
		return &WriteResult{Created: resp.Status == http.StatusCreated, ETag: resp.ETag}, nil
	}
}

// UpdateFile overwrites an existing file. etag must be the value ReadFile
// returned; the server answers 409 if someone changed the file since.
func (c *Client) UpdateFile(ctx context.Context, plane Plane, rel, body, etag string) (*WriteResult, error) {
	if etag == "" {
		return nil, errors.New("etag is required to change an existing file: read the file first")
	}
	p, err := CleanPath(rel)
	if err != nil {
		return nil, err
	}
	resp, err := c.Do(ctx, Request{
		Method: http.MethodPut, Path: "/" + string(plane) + "/files/" + p,
		Body: []byte(body), ContentType: contentType(rel), IfMatch: etag,
	})
	if err != nil {
		return nil, err
	}
	return &WriteResult{Created: resp.Status == http.StatusCreated, ETag: resp.ETag}, nil
}

// DeleteFile removes a file. etag is required for the same reason as UpdateFile.
func (c *Client) DeleteFile(ctx context.Context, plane Plane, rel, etag string) error {
	if etag == "" {
		return errors.New("etag is required to delete a file: read the file first")
	}
	p, err := CleanPath(rel)
	if err != nil {
		return err
	}
	_, err = c.Do(ctx, Request{
		Method: http.MethodDelete, Path: "/" + string(plane) + "/files/" + p, IfMatch: etag,
	})
	return err
}

func contentType(rel string) string {
	switch {
	case strings.HasSuffix(rel, ".json"):
		return "application/json; charset=utf-8"
	case strings.HasSuffix(rel, ".md"):
		return "text/markdown; charset=utf-8"
	}
	return "text/plain; charset=utf-8"
}

func isStatus(err error, status int) bool {
	var se *StatusError
	return errors.As(err, &se) && se.Status == status
}
