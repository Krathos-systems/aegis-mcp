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
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Krathos-systems/aegis-mcp/internal/aegis"
)

// levelRank orders Aegis's permission levels.
var levelRank = map[string]int{"reader": 1, "collaborator": 2, "editor": 3, "administrator": 4}

// WritesAllowed reports whether an account, as GET /admin/me describes it,
// holds editor anywhere — the least any write tool needs. For an API token the
// levels are already lowered to the token's limit.
func WritesAllowed(me map[string]any) bool {
	level, _ := me["highest_level"].(string)
	return levelRank[level] >= levelRank["editor"]
}

// TokenExpiry is when the API token expires, as GET /admin/me reports it.
// ok is false for a session, or an Aegis that does not report it.
func TokenExpiry(me map[string]any) (expires time.Time, ok bool) {
	token, _ := me["api_token"].(map[string]any)
	s, _ := token["expires_at"].(string)
	t, err := time.Parse(time.RFC3339, s)
	return t, err == nil
}

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

	add(s, &mcp.Tool{
		Name:        "aegis_list_activity",
		Description: "List recent changes to Aegis's files, newest first: who made each one, when, and which files it touched. Includes changes made through this server.",
		Annotations: readOnly("List activity"),
	}, h.listActivity, nil)

	add(s, &mcp.Tool{
		Name:        "aegis_show_change",
		Description: "Show the diff of one change from aegis_list_activity.",
		Annotations: readOnly("Show a change"),
	}, h.showChange, nil)

	add(s, &mcp.Tool{
		Name:        "aegis_compose_context",
		Description: "Compose a repository's ./CLAUDE.md the way Aegis delivers it to one developer (repository, teams, role, role-in-repository and personal sections, in order), plus the global file. drafts stand in for stored files, so a change can be checked before it is written. Reports lines per section against the budget, lines repeated across sections or with global, and the skills and agents each layer contributes.",
		Annotations: readOnly("Compose context"),
	}, h.composeContext, nil)

	addPrompt(s, healthCheckPrompt)

	if opts.ReadOnly {
		return
	}

	addPrompt(s, addRepoPrompt)
	addPrompt(s, addGuidancePrompt)

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
		Name:        "aegis_create_variable",
		Description: "Create a variable that files can reference as {{ aegis.NAME }}. kind \"cleartext\" is substituted into delivered files; kind \"secret\" is stored encrypted, can never be read back, and is only usable as the whole value of an env entry of an MCP server with a command in mcp.json. Only create a secret with a value the user gave you for that purpose, and do not repeat it.",
		Annotations: &mcp.ToolAnnotations{Title: "Create variable", DestructiveHint: ptr(false), OpenWorldHint: ptr(false)},
	}, h.createVariable, enum("kind", "cleartext", "secret"))

	add(s, &mcp.Tool{
		Name:        "aegis_update_variable",
		Description: "Change a variable's value, description or kind. Leave value out to keep the stored one (a secret's value cannot be read, so this is how to edit its description). Changing kind needs the value again.",
		Annotations: &mcp.ToolAnnotations{Title: "Update variable", DestructiveHint: ptr(true), IdempotentHint: true, OpenWorldHint: ptr(false)},
	}, h.updateVariable, enum("kind", "cleartext", "secret"))

	add(s, &mcp.Tool{
		Name:        "aegis_delete_variable",
		Description: "Delete a variable. Refused while files still reference it, unless force is true, in which case those references stop resolving until the files are fixed.",
		Annotations: &mcp.ToolAnnotations{Title: "Delete variable", DestructiveHint: ptr(true), IdempotentHint: true, OpenWorldHint: ptr(false)},
	}, h.deleteVariable, nil)

	add(s, &mcp.Tool{
		Name:        "aegis_register_repo",
		Description: "Register a git remote under a repository slug, so developers working in that repository receive its repos/<slug>/ files. A second remote for the same slug (a mirror or fork) is an alias.",
		Annotations: &mcp.ToolAnnotations{Title: "Register repository", DestructiveHint: ptr(false), OpenWorldHint: ptr(false)},
	}, h.registerRepo, nil)

	add(s, &mcp.Tool{
		Name:        "aegis_move_repo",
		Description: "Point a registered remote (by its id from aegis_list_repos) at a different repository slug.",
		Annotations: &mcp.ToolAnnotations{Title: "Move repository remote", DestructiveHint: ptr(true), IdempotentHint: true, OpenWorldHint: ptr(false)},
	}, h.moveRepo, nil)

	add(s, &mcp.Tool{
		Name:        "aegis_unregister_repo",
		Description: "Remove a registered remote (by its id). When it is the slug's last remote, developers in that repository stop receiving its files and the slug's permission grants are removed.",
		Annotations: &mcp.ToolAnnotations{Title: "Unregister repository", DestructiveHint: ptr(true), IdempotentHint: true, OpenWorldHint: ptr(false)},
	}, h.unregisterRepo, nil)

	add(s, &mcp.Tool{
		Name:        "aegis_scan_repo_import",
		Description: "Read a registered repository (by its id) and list the AI files in it that an import would bring into Aegis, which already exist, and which cannot be imported. Changes nothing. Returns the sha to pass to aegis_import_repo. The repository's read access must be set up in Aegis first.",
		Annotations: &mcp.ToolAnnotations{Title: "Scan repository for import", ReadOnlyHint: true, OpenWorldHint: ptr(true)},
	}, h.scanImport, nil)

	add(s, &mcp.Tool{
		Name:        "aegis_import_repo",
		Description: "Import the files aegis_scan_repo_import listed into the repository's layer, as one change. Files that already exist in Aegis are skipped unless named in replace. Refused if the repository changed since the scan; scan again.",
		Annotations: &mcp.ToolAnnotations{Title: "Import repository files", DestructiveHint: ptr(true), OpenWorldHint: ptr(true)},
	}, h.importRepo, nil)

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

var planeEnum = enum("plane", string(aegis.Content), string(aegis.Config))

// enum limits one string property of the input schema to values.
func enum(property string, values ...string) func(*jsonschema.Schema) {
	return func(s *jsonschema.Schema) {
		p := s.Properties[property]
		for _, v := range values {
			p.Enum = append(p.Enum, v)
		}
	}
}

func readOnly(title string) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{Title: title, ReadOnlyHint: true, OpenWorldHint: ptr(false)}
}

func ptr[T any](v T) *T { return &v }

// markdownLines is a content file's line count, the nudge a write gives
// towards the budget; 0 (left out) for anything else.
func markdownLines(plane aegis.Plane, path, body string) int {
	if plane != aegis.Content || !strings.HasSuffix(path, ".md") {
		return 0
	}
	return lineCount(body)
}

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

type activityIn struct {
	Limit  int `json:"limit,omitempty" jsonschema:"how many changes, at most 200 (default 50)"`
	Offset int `json:"offset,omitempty" jsonschema:"changes to skip, for paging"`
}

type changeIn struct {
	SHA string `json:"sha" jsonschema:"the change's sha from aegis_list_activity"`
}

type createVariableIn struct {
	Name        string `json:"name" jsonschema:"upper snake case, e.g. API_BASE_URL"`
	Kind        string `json:"kind" jsonschema:"cleartext or secret"`
	Value       string `json:"value"`
	Description string `json:"description,omitempty"`
}

type updateVariableIn struct {
	Name        string  `json:"name"`
	Value       *string `json:"value,omitempty" jsonschema:"the new value; leave out to keep the stored one"`
	Description *string `json:"description,omitempty" jsonschema:"the new description; leave out to keep it"`
	Kind        *string `json:"kind,omitempty" jsonschema:"cleartext or secret; changing it needs value"`
}

type deleteVariableIn struct {
	Name  string `json:"name"`
	Force bool   `json:"force,omitempty" jsonschema:"delete even though files still reference it"`
}

type registerRepoIn struct {
	RemoteURL string `json:"remote_url" jsonschema:"the git remote, e.g. https://github.com/acme/web.git or git@github.com:acme/web.git"`
	Repo      string `json:"repo" jsonschema:"the repository slug, e.g. web"`
}

type moveRepoIn struct {
	ID   int    `json:"id" jsonschema:"the remote's id from aegis_list_repos"`
	Repo string `json:"repo" jsonschema:"the slug to point it at"`
}

type repoIDIn struct {
	ID int `json:"id" jsonschema:"the remote's id from aegis_list_repos"`
}

type importIn struct {
	ID      int      `json:"id" jsonschema:"the remote's id from aegis_list_repos"`
	SHA     string   `json:"sha" jsonschema:"the sha aegis_scan_repo_import returned"`
	Replace []string `json:"replace,omitempty" jsonschema:"targets that already exist in Aegis to overwrite"`
}

type doneOut struct {
	Done bool `json:"done"`
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
	Lines   int    `json:"lines,omitempty" jsonschema:"lines in a Markdown file; check the composed size with aegis_compose_context"`
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
	return nil, writeOut{Plane: string(plane), Path: in.Path, Created: true, ETag: r.ETag, Lines: markdownLines(plane, in.Path, in.Body)}, nil
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
	return nil, writeOut{Plane: string(plane), Path: in.Path, ETag: r.ETag, Lines: markdownLines(plane, in.Path, in.Body)}, nil
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

func (h *handlers) listActivity(ctx context.Context, _ *mcp.CallToolRequest, in activityIn) (*mcp.CallToolResult, map[string]any, error) {
	return result(h.c.Activity(ctx, in.Limit, in.Offset))
}

func (h *handlers) showChange(ctx context.Context, _ *mcp.CallToolRequest, in changeIn) (*mcp.CallToolResult, map[string]any, error) {
	return result(h.c.Change(ctx, in.SHA))
}

func (h *handlers) createVariable(ctx context.Context, _ *mcp.CallToolRequest, in createVariableIn) (*mcp.CallToolResult, map[string]any, error) {
	return result(h.c.CreateVariable(ctx, in.Name, aegis.VariableChange{
		Kind: &in.Kind, Value: &in.Value, Description: &in.Description,
	}))
}

func (h *handlers) updateVariable(ctx context.Context, _ *mcp.CallToolRequest, in updateVariableIn) (*mcp.CallToolResult, map[string]any, error) {
	change := aegis.VariableChange{Kind: in.Kind, Value: in.Value, Description: in.Description}
	if change.Description == nil {
		// Aegis replaces the description with whatever is sent, empty included,
		// so keep the current one unless asked to change it.
		current, err := h.c.Variable(ctx, in.Name)
		if err != nil {
			return nil, nil, explain(err)
		}
		description, _ := current["description"].(string)
		change.Description = &description
	}
	return result(h.c.UpdateVariable(ctx, in.Name, change))
}

func (h *handlers) deleteVariable(ctx context.Context, _ *mcp.CallToolRequest, in deleteVariableIn) (*mcp.CallToolResult, doneOut, error) {
	if err := h.c.DeleteVariable(ctx, in.Name, in.Force); err != nil {
		return nil, doneOut{}, explain(err)
	}
	return nil, doneOut{Done: true}, nil
}

func (h *handlers) registerRepo(ctx context.Context, _ *mcp.CallToolRequest, in registerRepoIn) (*mcp.CallToolResult, map[string]any, error) {
	return result(h.c.RegisterRepo(ctx, in.RemoteURL, in.Repo))
}

func (h *handlers) moveRepo(ctx context.Context, _ *mcp.CallToolRequest, in moveRepoIn) (*mcp.CallToolResult, map[string]any, error) {
	return result(h.c.MoveRepo(ctx, in.ID, in.Repo))
}

func (h *handlers) unregisterRepo(ctx context.Context, _ *mcp.CallToolRequest, in repoIDIn) (*mcp.CallToolResult, doneOut, error) {
	if err := h.c.UnregisterRepo(ctx, in.ID); err != nil {
		return nil, doneOut{}, explain(err)
	}
	return nil, doneOut{Done: true}, nil
}

func (h *handlers) scanImport(ctx context.Context, _ *mcp.CallToolRequest, in repoIDIn) (*mcp.CallToolResult, map[string]any, error) {
	return result(h.c.ScanImport(ctx, in.ID))
}

func (h *handlers) importRepo(ctx context.Context, _ *mcp.CallToolRequest, in importIn) (*mcp.CallToolResult, map[string]any, error) {
	return result(h.c.Import(ctx, in.ID, in.SHA, in.Replace))
}

// result adapts a client call that returns a JSON object to a tool result.
func result(v map[string]any, err error) (*mcp.CallToolResult, map[string]any, error) {
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
		hint = "the token was refused: it may be mistyped, expired or revoked. The user can make a new API token on their Aegis account page and run `aegis-mcp setup` with it"
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
		hint = "check that it exists with the matching aegis_list_ tool"
	case http.StatusConflict:
		_, rescan := se.Body["sha"]
		_, referenced := se.Body["used_in"]
		switch {
		case rescan:
			hint = "run aegis_scan_repo_import again and import with the new sha"
		case referenced:
			hint = "change the files that reference it first, or delete it with force"
		case strings.Contains(msg, "already"):
			hint = "it already exists"
		default:
			hint = "the file changed since it was read; read it again, reapply the change, and retry with the new etag"
		}
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
