package aegis

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// ---- activity ----

// Activity returns the history of changes, newest first (GET /activity).
// limit and offset are left to the server's defaults when zero.
func (c *Client) Activity(ctx context.Context, limit, offset int) (map[string]any, error) {
	q := url.Values{}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if offset > 0 {
		q.Set("offset", strconv.Itoa(offset))
	}
	var out map[string]any
	_, err := c.JSON(ctx, Request{Method: http.MethodGet, Path: "/activity", Query: q}, &out)
	return out, err
}

// Change returns one change's diff (GET /activity/<sha>).
func (c *Client) Change(ctx context.Context, sha string) (map[string]any, error) {
	if !isHex(sha, 4, 40) {
		return nil, fmt.Errorf("%q is not a commit id: use the sha from the activity feed", sha)
	}
	var out map[string]any
	_, err := c.JSON(ctx, Request{Method: http.MethodGet, Path: "/activity/" + sha}, &out)
	return out, err
}

// ---- variables ----

// VariableChange is a variable write. A nil field is left out of the request.
type VariableChange struct {
	Kind        *string `json:"kind,omitempty"`
	Value       *string `json:"value,omitempty"`
	Description *string `json:"description,omitempty"`
}

// CreateVariable creates a variable (POST /variables).
func (c *Client) CreateVariable(ctx context.Context, name string, v VariableChange) (map[string]any, error) {
	body := map[string]any{"name": name}
	addVariableFields(body, v)
	var out map[string]any
	_, err := c.SendJSON(ctx, Request{Method: http.MethodPost, Path: "/variables"}, body, &out)
	return out, err
}

// UpdateVariable changes a variable (PUT /variables/<name>). Aegis keeps the
// stored value when Value is nil, but replaces the description with whatever
// is sent, so the caller must pass the current one to keep it.
func (c *Client) UpdateVariable(ctx context.Context, name string, v VariableChange) (map[string]any, error) {
	body := map[string]any{}
	addVariableFields(body, v)
	var out map[string]any
	_, err := c.SendJSON(ctx, Request{Method: http.MethodPut, Path: "/variables/" + url.PathEscape(name)}, body, &out)
	return out, err
}

// DeleteVariable deletes a variable (DELETE /variables/<name>). Without force,
// Aegis refuses while files still reference it.
func (c *Client) DeleteVariable(ctx context.Context, name string, force bool) error {
	r := Request{Method: http.MethodDelete, Path: "/variables/" + url.PathEscape(name)}
	if force {
		r.Query = url.Values{"force": {"1"}}
	}
	_, err := c.Do(ctx, r)
	return err
}

// Variable returns one variable from the listing, or an error if there is none.
func (c *Client) Variable(ctx context.Context, name string) (map[string]any, error) {
	all, err := c.Variables(ctx)
	if err != nil {
		return nil, err
	}
	list, _ := all["variables"].([]any)
	for _, item := range list {
		if v, ok := item.(map[string]any); ok && v["name"] == name {
			return v, nil
		}
	}
	return nil, &StatusError{Method: http.MethodGet, Path: "/variables", Status: http.StatusNotFound,
		Message: fmt.Sprintf("no variable named %s", name)}
}

func addVariableFields(body map[string]any, v VariableChange) {
	if v.Kind != nil {
		body["kind"] = *v.Kind
	}
	if v.Value != nil {
		body["value"] = *v.Value
	}
	if v.Description != nil {
		body["description"] = *v.Description
	}
}

// ---- repositories ----

// RegisterRepo maps a git remote to a repository slug (POST /repos).
func (c *Client) RegisterRepo(ctx context.Context, remoteURL, repo string) (map[string]any, error) {
	var out map[string]any
	_, err := c.SendJSON(ctx, Request{Method: http.MethodPost, Path: "/repos"},
		map[string]string{"remote_url": remoteURL, "repo": repo}, &out)
	return out, err
}

// MoveRepo points a registered remote at another slug (PATCH /repos/<id>).
func (c *Client) MoveRepo(ctx context.Context, id int, repo string) (map[string]any, error) {
	var out map[string]any
	_, err := c.SendJSON(ctx, Request{Method: http.MethodPatch, Path: repoPath(id, "")},
		map[string]string{"repo": repo}, &out)
	return out, err
}

// UnregisterRepo removes a registered remote (DELETE /repos/<id>).
func (c *Client) UnregisterRepo(ctx context.Context, id int) error {
	_, err := c.Do(ctx, Request{Method: http.MethodDelete, Path: repoPath(id, "")})
	return err
}

// ScanImport lists what importing a repository's files would write, and the
// commit it read (POST /repos/<id>/import/scan). It writes nothing.
func (c *Client) ScanImport(ctx context.Context, id int) (map[string]any, error) {
	var out map[string]any
	_, err := c.JSON(ctx, Request{Method: http.MethodPost, Path: repoPath(id, "/import/scan"), Timeout: LongTimeout}, &out)
	return out, err
}

// Import copies a repository's files into Aegis as one change
// (POST /repos/<id>/import). sha must come from ScanImport; replace names the
// existing files to overwrite, and every other existing file is skipped.
func (c *Client) Import(ctx context.Context, id int, sha string, replace []string) (map[string]any, error) {
	if !isHex(sha, 40, 64) {
		return nil, errors.New("sha must be the full commit id from the scan")
	}
	if replace == nil {
		replace = []string{}
	}
	var out map[string]any
	_, err := c.SendJSON(ctx, Request{Method: http.MethodPost, Path: repoPath(id, "/import"), Timeout: LongTimeout},
		map[string]any{"sha": sha, "replace": replace}, &out)
	return out, err
}

func repoPath(id int, suffix string) string {
	return "/repos/" + strconv.Itoa(id) + suffix
}

func isHex(s string, min, max int) bool {
	if len(s) < min || len(s) > max {
		return false
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F') {
			return false
		}
	}
	return true
}
