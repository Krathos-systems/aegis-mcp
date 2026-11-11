// Package tools defines the MCP tools and the instructions the server sends
// to a connecting client. Each tool is a thin wrapper over one Aegis admin
// API call in package aegis; the server enforces every permission, so a tool
// never decides on its own what the caller may do.
package tools

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Krathos-systems/aegis-mcp/internal/aegis"
)

// Options controls which tools are registered.
type Options struct {
	// ReadOnly leaves out every tool that changes something in Aegis.
	ReadOnly bool
}

// Register adds the tools to s.
func Register(s *mcp.Server, c *aegis.Client, opts Options) {
	h := &handlers{c: c}

	add(s, &mcp.Tool{
		Name:        "aegis_whoami",
		Description: "Show which Aegis account the token belongs to, its permission levels, and the server's version. Call this first to check the connection.",
		Annotations: readOnly("Who am I"),
	}, h.whoami, nil)

	add(s, &mcp.Tool{
		Name:        "aegis_list_files",
		Description: "List the managed files in one plane that this account may read. plane is \"content\" (CLAUDE.md, skills, agents) or \"config\" (settings.json, mcp.json).",
		Annotations: readOnly("List files"),
	}, h.listFiles, planeEnum)

	add(s, &mcp.Tool{
		Name:        "aegis_read_file",
		Description: "Read one managed file as stored, with {{ aegis.NAME }} variable placeholders left in place. Returns the etag needed to update or delete it.",
		Annotations: readOnly("Read file"),
	}, h.readFile, planeEnum)

	add(s, &mcp.Tool{
		Name:        "aegis_list_repos",
		Description: "List the repositories registered in Aegis, with their slugs and git remotes. The slug is the <repo> segment in repos/<repo>/ paths.",
		Annotations: readOnly("List repositories"),
	}, h.listRepos, nil)

	add(s, &mcp.Tool{
		Name:        "aegis_list_variables",
		Description: "List variables (referenced in files as {{ aegis.NAME }}) and which files use each one. Secret values are never returned.",
		Annotations: readOnly("List variables"),
	}, h.listVariables, nil)

	add(s, &mcp.Tool{
		Name:        "aegis_preview_role",
		Description: "Preview what a developer holding one role receives in one repository after every layer is merged: instructions, skills, agents, settings and MCP servers. Use it to check a change.",
		Annotations: readOnly("Preview a role"),
	}, h.previewRole, nil)

	if opts.ReadOnly {
		return
	}

	add(s, &mcp.Tool{
		Name:        "aegis_create_file",
		Description: "Create a new managed file. Fails if the file already exists; use aegis_update_file for that. Every change is recorded in Aegis's history under this account.",
		Annotations: &mcp.ToolAnnotations{Title: "Create file", DestructiveHint: ptr(false), OpenWorldHint: ptr(false)},
	}, h.createFile, planeEnum)

	add(s, &mcp.Tool{
		Name:        "aegis_update_file",
		Description: "Replace the whole body of an existing managed file. etag must come from aegis_read_file; if the file changed since, the update is refused and you should read it again.",
		Annotations: &mcp.ToolAnnotations{Title: "Update file", DestructiveHint: ptr(true), IdempotentHint: true, OpenWorldHint: ptr(false)},
	}, h.updateFile, planeEnum)

	add(s, &mcp.Tool{
		Name:        "aegis_delete_file",
		Description: "Delete a managed file. etag must come from aegis_read_file. Developers stop receiving the file on their next sync.",
		Annotations: &mcp.ToolAnnotations{Title: "Delete file", DestructiveHint: ptr(true), IdempotentHint: true, OpenWorldHint: ptr(false)},
	}, h.deleteFile, planeEnum)
}

// add registers a typed tool. adjust, if given, edits the inferred input schema.
func add[In, Out any](s *mcp.Server, t *mcp.Tool, h mcp.ToolHandlerFor[In, Out], adjust func(*jsonschema.Schema)) {
	if adjust != nil {
		schema, err := jsonschema.For[In](nil)
		if err != nil {
			panic(fmt.Sprintf("tool %s: %v", t.Name, err))
		}
		adjust(schema)
		t.InputSchema = schema
	}
	mcp.AddTool(s, t, h)
}

func planeEnum(s *jsonschema.Schema) {
	if p, ok := s.Properties["plane"]; ok {
		p.Enum = []any{string(aegis.Content), string(aegis.Config)}
	}
}

func readOnly(title string) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{Title: title, ReadOnlyHint: true, OpenWorldHint: ptr(false)}
}

func ptr[T any](v T) *T { return &v }

type handlers struct{ c *aegis.Client }

// ---- inputs and outputs ----

type empty struct{}

type planeIn struct {
	Plane string `json:"plane" jsonschema:"content or config"`
}

type fileIn struct {
	Plane string `json:"plane" jsonschema:"content or config"`
	Path  string `json:"path" jsonschema:"path inside the plane, e.g. roles/backend/claude/CLAUDE.md"`
}

type createIn struct {
	Plane string `json:"plane" jsonschema:"content or config"`
	Path  string `json:"path" jsonschema:"path inside the plane, e.g. teams/payments/claude/CLAUDE.md"`
	Body  string `json:"body" jsonschema:"the whole file; .json files in the config plane must be valid JSON"`
}

type updateIn struct {
	Plane string `json:"plane" jsonschema:"content or config"`
	Path  string `json:"path" jsonschema:"path inside the plane"`
	Body  string `json:"body" jsonschema:"the whole new file, not a diff"`
	ETag  string `json:"etag" jsonschema:"the etag aegis_read_file returned for this file"`
}

type deleteIn struct {
	Plane string `json:"plane" jsonschema:"content or config"`
	Path  string `json:"path" jsonschema:"path inside the plane"`
	ETag  string `json:"etag" jsonschema:"the etag aegis_read_file returned for this file"`
}

type previewIn struct {
	Role string `json:"role" jsonschema:"role name, as in roles/<role>/"`
	Repo string `json:"repo" jsonschema:"repository slug, from aegis_list_repos"`
}

type whoamiOut struct {
	Server  string         `json:"server"`
	Account map[string]any `json:"account"`
	Health  map[string]any `json:"health,omitempty"`
}

type listFilesOut struct {
	Plane string   `json:"plane"`
	Files []string `json:"files"`
}

type fileOut struct {
	Plane string `json:"plane"`
	Path  string `json:"path"`
	ETag  string `json:"etag"`
	Body  string `json:"body"`
}

type writeOut struct {
	Plane   string `json:"plane"`
	Path    string `json:"path"`
	Created bool   `json:"created"`
	ETag    string `json:"etag" jsonschema:"use this etag for the next update"`
}

type deleteOut struct {
	Plane   string `json:"plane"`
	Path    string `json:"path"`
	Deleted bool   `json:"deleted"`
}

type reposOut struct {
	Repos any `json:"repos"`
}

// ---- handlers ----

func (h *handlers) whoami(ctx context.Context, _ *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, whoamiOut, error) {
	me, err := h.c.Me(ctx)
	if err != nil {
		return nil, whoamiOut{}, explain(err)
	}
	out := whoamiOut{Server: h.c.BaseURL(), Account: me}
	// Health is informational; a failure here does not fail the call.
	if hv, err := h.c.Health(ctx); err == nil {
		out.Health = hv
	}
	return nil, out, nil
}

func (h *handlers) listFiles(ctx context.Context, _ *mcp.CallToolRequest, in planeIn) (*mcp.CallToolResult, listFilesOut, error) {
	plane, err := aegis.ParsePlane(in.Plane)
	if err != nil {
		return nil, listFilesOut{}, err
	}
	files, err := h.c.ListFiles(ctx, plane)
	if err != nil {
		return nil, listFilesOut{}, explain(err)
	}
	if files == nil {
		files = []string{}
	}
	return nil, listFilesOut{Plane: string(plane), Files: files}, nil
}

func (h *handlers) readFile(ctx context.Context, _ *mcp.CallToolRequest, in fileIn) (*mcp.CallToolResult, fileOut, error) {
	plane, err := aegis.ParsePlane(in.Plane)
	if err != nil {
		return nil, fileOut{}, err
	}
	f, err := h.c.ReadFile(ctx, plane, in.Path)
	if err != nil {
		return nil, fileOut{}, explain(err)
	}
	return nil, fileOut{Plane: string(plane), Path: in.Path, ETag: f.ETag, Body: f.Body}, nil
}

func (h *handlers) createFile(ctx context.Context, _ *mcp.CallToolRequest, in createIn) (*mcp.CallToolResult, writeOut, error) {
	plane, err := aegis.ParsePlane(in.Plane)
	if err != nil {
		return nil, writeOut{}, err
	}
	r, err := h.c.CreateFile(ctx, plane, in.Path, in.Body)
	if err != nil {
		if errors.Is(err, aegis.ErrExists) {
			return nil, writeOut{}, fmt.Errorf("%w; read it and use aegis_update_file instead", err)
		}
		return nil, writeOut{}, explain(err)
	}
	return nil, writeOut{Plane: string(plane), Path: in.Path, Created: true, ETag: r.ETag}, nil
}

func (h *handlers) updateFile(ctx context.Context, _ *mcp.CallToolRequest, in updateIn) (*mcp.CallToolResult, writeOut, error) {
	plane, err := aegis.ParsePlane(in.Plane)
	if err != nil {
		return nil, writeOut{}, err
	}
	// Content PUT refuses a missing file; config PUT would create one. Keep
	// "update" meaning the same in both planes: the file must already exist.
	r, err := h.c.UpdateFile(ctx, plane, in.Path, in.Body, in.ETag)
	if err != nil {
		return nil, writeOut{}, explain(err)
	}
	if r.Created {
		return nil, writeOut{}, fmt.Errorf("%s/%s did not exist and was created; use aegis_create_file for new files", plane, in.Path)
	}
	return nil, writeOut{Plane: string(plane), Path: in.Path, ETag: r.ETag}, nil
}

func (h *handlers) deleteFile(ctx context.Context, _ *mcp.CallToolRequest, in deleteIn) (*mcp.CallToolResult, deleteOut, error) {
	plane, err := aegis.ParsePlane(in.Plane)
	if err != nil {
		return nil, deleteOut{}, err
	}
	if err := h.c.DeleteFile(ctx, plane, in.Path, in.ETag); err != nil {
		return nil, deleteOut{}, explain(err)
	}
	return nil, deleteOut{Plane: string(plane), Path: in.Path, Deleted: true}, nil
}

func (h *handlers) listRepos(ctx context.Context, _ *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, reposOut, error) {
	repos, err := h.c.Repos(ctx)
	if err != nil {
		return nil, reposOut{}, explain(err)
	}
	return nil, reposOut{Repos: repos}, nil
}

func (h *handlers) listVariables(ctx context.Context, _ *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, map[string]any, error) {
	v, err := h.c.Variables(ctx)
	if err != nil {
		return nil, nil, explain(err)
	}
	return nil, v, nil
}

func (h *handlers) previewRole(ctx context.Context, _ *mcp.CallToolRequest, in previewIn) (*mcp.CallToolResult, map[string]any, error) {
	if strings.TrimSpace(in.Role) == "" || strings.TrimSpace(in.Repo) == "" {
		return nil, nil, errors.New("role and repo are both required")
	}
	v, err := h.c.PreviewRole(ctx, in.Role, in.Repo)
	if err != nil {
		return nil, nil, explain(err)
	}
	return nil, v, nil
}

// explain turns an Aegis error into a sentence a model can act on. The
// server's own message is kept; the hint says what to do next.
func explain(err error) error {
	var se *aegis.StatusError
	if !errors.As(err, &se) {
		return err
	}
	msg := se.Message
	if msg == "" {
		msg = http.StatusText(se.Status)
	}
	var hint string
	switch se.Status {
	case http.StatusUnauthorized:
		hint = "the token was refused; it may be wrong, expired or revoked. Ask the user for a new one"
	case http.StatusForbidden:
		if req, ok := se.Body["required"].(string); ok {
			held, _ := se.Body["held"].(string)
			if held == "" {
				held = "none"
			}
			hint = fmt.Sprintf("this account needs the %s level here and holds %s", req, held)
		} else {
			hint = "this account is not allowed to do that"
		}
	case http.StatusNotFound:
		hint = "check the path with aegis_list_files"
	case http.StatusConflict:
		hint = "the file changed since it was read; read it again, reapply the change, and retry with the new etag"
	case http.StatusPaymentRequired:
		hint = "the organisation's plan limit is reached"
	case http.StatusRequestEntityTooLarge:
		hint = "a storage limit is reached"
	case http.StatusTooManyRequests:
		hint = "rate limited; wait a minute before retrying"
	case http.StatusLocked, http.StatusGone, http.StatusServiceUnavailable:
		if state, ok := se.Body["org_state"].(string); ok {
			hint = "the organisation is " + strings.ReplaceAll(state, "_", " ")
		}
	}
	if hint == "" {
		return fmt.Errorf("Aegis refused the request (%d): %s", se.Status, msg)
	}
	return fmt.Errorf("Aegis refused the request (%d): %s. %s", se.Status, msg, hint)
}
