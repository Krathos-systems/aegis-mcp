package aegis

import (
	"context"
	"net/http"
	"net/url"
)

// Me returns the signed-in principal and its permission levels (GET /admin/me).
func (c *Client) Me(ctx context.Context) (map[string]any, error) {
	var out map[string]any
	_, err := c.JSON(ctx, Request{Method: http.MethodGet, Path: "/admin/me"}, &out)
	return out, err
}

// Repos lists the registered repositories (GET /repos).
func (c *Client) Repos(ctx context.Context) (any, error) {
	var out any
	_, err := c.JSON(ctx, Request{Method: http.MethodGet, Path: "/repos"}, &out)
	return out, err
}

// Variables lists variables and where they are used (GET /variables).
// Secret values are never part of this response.
func (c *Client) Variables(ctx context.Context) (map[string]any, error) {
	var out map[string]any
	_, err := c.JSON(ctx, Request{Method: http.MethodGet, Path: "/variables"}, &out)
	return out, err
}

// PreviewRole returns what one role resolves to in one repository
// (GET /admin/preview/role).
func (c *Client) PreviewRole(ctx context.Context, role, repo string) (map[string]any, error) {
	var out map[string]any
	_, err := c.JSON(ctx, Request{
		Method: http.MethodGet,
		Path:   "/admin/preview/role",
		Query:  url.Values{"role": {role}, "repo": {repo}},
	}, &out)
	return out, err
}
