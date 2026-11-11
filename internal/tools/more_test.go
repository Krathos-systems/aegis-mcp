package tools

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Krathos-systems/aegis-mcp/internal/aegis"
)

// routes answers each "METHOD /path" with a fixed status and body; anything
// else is a 500.
type routes map[string]struct {
	status int
	body   string
}

func (rt routes) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	resp, ok := rt[r.Method+" "+r.URL.Path]
	if !ok {
		w.WriteHeader(http.StatusInternalServerError)
		io.WriteString(w, `{"error":"unexpected request"}`)
		return
	}
	w.Header().Set("ETag", `"e"`)
	w.WriteHeader(resp.status)
	io.WriteString(w, resp.body)
}

// failing answers every request with one status and body.
func failing(status int, body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		io.WriteString(w, body)
	})
}

func TestWhoami(t *testing.T) {
	cs := connectTo(t, routes{
		"GET /api/admin/me": {200, `{"name":"ada","level":"editor"}`},
		"GET /health":       {200, `{"status":"ok","versions":{"api":"v2.10.1"}}`},
	}, "x", false)
	out, text, isErr := call(t, cs, "aegis_whoami", nil)
	if isErr {
		t.Fatal(text)
	}
	acct, _ := out["account"].(map[string]any)
	health, _ := out["health"].(map[string]any)
	if acct["name"] != "ada" || health["status"] != "ok" || !strings.HasPrefix(out["server"].(string), "http://") {
		t.Fatalf("out = %v", out)
	}
}

func TestWhoamiWithoutHealth(t *testing.T) {
	cs := connectTo(t, routes{"GET /api/admin/me": {200, `{"name":"ada"}`}}, "x", false)
	out, text, isErr := call(t, cs, "aegis_whoami", nil)
	if isErr {
		t.Fatal(text)
	}
	if _, ok := out["health"]; ok {
		t.Fatalf("health should be omitted when /health fails: %v", out)
	}
}

func TestListFiles(t *testing.T) {
	cs, _ := connect(t, "good", false)
	out, text, isErr := call(t, cs, "aegis_list_files", map[string]any{"plane": "content"})
	if isErr {
		t.Fatal(text)
	}
	files, _ := out["files"].([]any)
	if out["plane"] != "content" || len(files) != 1 || files[0] != "global/claude/CLAUDE.md" {
		t.Fatalf("out = %v", out)
	}

	// An empty plane is [] rather than null, so the output matches its schema.
	out, text, isErr = call(t, cs, "aegis_list_files", map[string]any{"plane": "config"})
	if isErr {
		t.Fatal(text)
	}
	if files, ok := out["files"].([]any); !ok || len(files) != 0 {
		t.Fatalf("out = %v", out)
	}
}

func TestToolsReportServerErrors(t *testing.T) {
	cs := connectTo(t, failing(403, `{"error":"forbidden","required":"editor","held":"reader"}`), "x", false)
	cases := map[string]map[string]any{
		"aegis_whoami":         nil,
		"aegis_list_files":     {"plane": "content"},
		"aegis_read_file":      {"plane": "content", "path": "a.md"},
		"aegis_create_file":    {"plane": "content", "path": "a.md", "body": "x"},
		"aegis_update_file":    {"plane": "content", "path": "a.md", "body": "x", "etag": `"e"`},
		"aegis_delete_file":    {"plane": "content", "path": "a.md", "etag": `"e"`},
		"aegis_list_repos":     nil,
		"aegis_list_variables": nil,
		"aegis_preview_role":   {"role": "be", "repo": "web"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			_, text, isErr := call(t, cs, name, args)
			if !isErr || !strings.Contains(text, "needs the editor level here and holds reader") {
				t.Fatalf("isErr=%v %s", isErr, text)
			}
		})
	}
}

func TestToolsRefuseUnknownPlane(t *testing.T) {
	cs, _ := connect(t, "good", false)
	cases := map[string]map[string]any{
		"aegis_list_files":  {"plane": "secrets"},
		"aegis_read_file":   {"plane": "secrets", "path": "a.md"},
		"aegis_create_file": {"plane": "secrets", "path": "a.md", "body": "x"},
		"aegis_update_file": {"plane": "secrets", "path": "a.md", "body": "x", "etag": `"e"`},
		"aegis_delete_file": {"plane": "secrets", "path": "a.md", "etag": `"e"`},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, isErr := call(t, cs, name, args); !isErr {
				t.Fatal("accepted")
			}
		})
	}
}

func TestPlaneIsAnEnumInTheSchema(t *testing.T) {
	cs, _ := connect(t, "good", false)
	res, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range res.Tools {
		props, _ := tl.InputSchema.(map[string]any)["properties"].(map[string]any)
		plane, ok := props["plane"].(map[string]any)
		if !ok {
			continue
		}
		enum, _ := plane["enum"].([]any)
		if len(enum) != 2 {
			t.Errorf("%s: plane enum = %v", tl.Name, plane["enum"])
		}
	}
}

func TestUpdateThatCreatesIsReported(t *testing.T) {
	cs := connectTo(t, routes{"PUT /api/config/files/a.json": {201, `{"message":"created"}`}}, "x", false)
	_, text, isErr := call(t, cs, "aegis_update_file", map[string]any{
		"plane": "config", "path": "a.json", "body": "{}", "etag": `"e"`,
	})
	if !isErr || !strings.Contains(text, "aegis_create_file") {
		t.Fatalf("isErr=%v %s", isErr, text)
	}
}

func TestListReposAndVariables(t *testing.T) {
	cs := connectTo(t, routes{
		"GET /api/repos":     {200, `[{"slug":"web","remotes":["https://example.com/web.git"]}]`},
		"GET /api/variables": {200, `{"variables":[{"name":"API_URL","used_in":[]}],"secrets_available":true}`},
	}, "x", false)

	out, text, isErr := call(t, cs, "aegis_list_repos", nil)
	if isErr {
		t.Fatal(text)
	}
	if repos, _ := out["repos"].([]any); len(repos) != 1 {
		t.Fatalf("repos = %v", out)
	}

	out, text, isErr = call(t, cs, "aegis_list_variables", nil)
	if isErr {
		t.Fatal(text)
	}
	if out["secrets_available"] != true {
		t.Fatalf("variables = %v", out)
	}
}

func TestPreviewRole(t *testing.T) {
	cs := connectTo(t, routes{"GET /api/admin/preview/role": {200, `{"instructions":"# hi"}`}}, "x", false)
	out, text, isErr := call(t, cs, "aegis_preview_role", map[string]any{"role": "be", "repo": "web"})
	if isErr || out["instructions"] != "# hi" {
		t.Fatalf("isErr=%v %s %v", isErr, text, out)
	}
	_, text, isErr = call(t, cs, "aegis_preview_role", map[string]any{"role": " ", "repo": "web"})
	if !isErr || !strings.Contains(text, "required") {
		t.Fatalf("blank role: isErr=%v %s", isErr, text)
	}
}

func TestExplain(t *testing.T) {
	plain := errors.New("dial tcp: refused")
	if explain(plain) != plain {
		t.Error("a transport error was rewritten")
	}
	cases := []struct {
		err  *aegis.StatusError
		want string
	}{
		{&aegis.StatusError{Status: 401, Message: "token rejected"}, "Ask the user for a new one"},
		{&aegis.StatusError{Status: 403, Body: map[string]any{"required": "editor"}}, "holds none"},
		{&aegis.StatusError{Status: 403, Message: "nope", Body: map[string]any{}}, "not allowed"},
		{&aegis.StatusError{Status: 404, Message: "file not found"}, "aegis_list_files"},
		{&aegis.StatusError{Status: 409}, "read it again"},
		{&aegis.StatusError{Status: 402}, "plan limit"},
		{&aegis.StatusError{Status: 413}, "storage limit"},
		{&aegis.StatusError{Status: 429}, "wait a minute"},
		{&aegis.StatusError{Status: 423, Body: map[string]any{"org_state": "read_only"}}, "organisation is read only"},
		{&aegis.StatusError{Status: 410, Body: map[string]any{"org_state": "pending_deletion"}}, "pending deletion"},
		{&aegis.StatusError{Status: 503}, "(503): Service Unavailable"},
		{&aegis.StatusError{Status: 500, Message: "boom"}, "(500): boom"},
	}
	for _, c := range cases {
		got := explain(c.err).Error()
		if !strings.Contains(got, c.want) {
			t.Errorf("explain(%d) = %q; want it to contain %q", c.err.Status, got, c.want)
		}
	}
}

func TestAddPanicsOnAnImpossibleSchema(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("no panic")
		}
	}()
	type bad struct {
		C chan int `json:"c"`
	}
	s := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "t"}, nil)
	add(s, &mcp.Tool{Name: "bad"}, func(_ context.Context, _ *mcp.CallToolRequest, _ bad) (*mcp.CallToolResult, any, error) {
		return nil, nil, nil
	}, func(*jsonschema.Schema) {})
}

// Nothing a customer sees may describe how Aegis is run (see CLAUDE.md).
func TestNoInternalWordsInWhatClientsSee(t *testing.T) {
	cs, _ := connect(t, "good", false)
	res, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	seen := []string{Instructions, cs.InitializeResult().Instructions}
	for _, tl := range res.Tools {
		seen = append(seen, tl.Name, tl.Description)
	}
	all := strings.ToLower(strings.Join(seen, "\n"))
	for _, word := range []string{"self-host", "selfhost", "this instance", "this server is run", ".env", "environment variable", "operator"} {
		if strings.Contains(all, word) {
			t.Errorf("client-visible text mentions %q", word)
		}
	}
}

// The input schema already limits plane to two values, so these checks only
// run if a client skips validation. Call the handlers directly to prove the
// second guard holds.
func TestHandlersRefuseUnknownPlaneDirectly(t *testing.T) {
	h := &handlers{c: aegis.New("http://127.0.0.1:1", "x", "")}
	ctx := t.Context()
	if _, _, err := h.listFiles(ctx, nil, planeIn{Plane: "x"}); err == nil {
		t.Error("listFiles")
	}
	if _, _, err := h.readFile(ctx, nil, fileIn{Plane: "x"}); err == nil {
		t.Error("readFile")
	}
	if _, _, err := h.createFile(ctx, nil, createIn{Plane: "x"}); err == nil {
		t.Error("createFile")
	}
	if _, _, err := h.updateFile(ctx, nil, updateIn{Plane: "x"}); err == nil {
		t.Error("updateFile")
	}
	if _, _, err := h.deleteFile(ctx, nil, deleteIn{Plane: "x"}); err == nil {
		t.Error("deleteFile")
	}
}
