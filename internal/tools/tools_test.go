package tools

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Krathos-systems/aegis-mcp/internal/aegis"
)

// fakeAegis is a minimal stand-in for the Aegis admin API: two planes of
// files with ETags and If-Match, and /admin/me.
type fakeAegis struct {
	mu    sync.Mutex
	files map[string]string // "<plane>/<path>" -> body
}

func etag(body string) string { return fmt.Sprintf(`"%x"`, sha256.Sum256([]byte(body))) }

func (f *fakeAegis) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer good" {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":"token rejected"}`)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/")
	switch {
	case path == "admin/me":
		io.WriteString(w, `{"name":"ada","level":"editor"}`)
		return
	case path == "content/files" || path == "config/files":
		plane := strings.TrimSuffix(path, "/files")
		var out []string
		for k := range f.files {
			if strings.HasPrefix(k, plane+"/") {
				out = append(out, strings.TrimPrefix(k, plane+"/"))
			}
		}
		sort.Strings(out)
		json.NewEncoder(w).Encode(out)
		return
	}
	plane, rel, _ := strings.Cut(path, "/files/")
	key := plane + "/" + rel
	body, exists := f.files[key]
	if m := r.Header.Get("If-Match"); m != "" && (!exists || m != etag(body)) {
		w.WriteHeader(http.StatusConflict)
		io.WriteString(w, `{"error":"changed by someone else"}`)
		return
	}
	data, _ := io.ReadAll(r.Body)
	switch r.Method {
	case http.MethodGet:
		if !exists {
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"error":"file not found"}`)
			return
		}
		w.Header().Set("ETag", etag(body))
		io.WriteString(w, body)
	case http.MethodPost:
		if exists {
			w.WriteHeader(http.StatusConflict)
			io.WriteString(w, `{"error":"file already exists"}`)
			return
		}
		f.files[key] = string(data)
		w.Header().Set("ETag", etag(string(data)))
		w.WriteHeader(http.StatusCreated)
	case http.MethodPut:
		if plane == "content" && !exists {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		f.files[key] = string(data)
		w.Header().Set("ETag", etag(string(data)))
		if !exists {
			w.WriteHeader(http.StatusCreated)
		}
	case http.MethodDelete:
		delete(f.files, key)
		w.WriteHeader(http.StatusNoContent)
	}
}

func connect(t *testing.T, token string, readOnly bool) (*mcp.ClientSession, *fakeAegis) {
	t.Helper()
	fake := &fakeAegis{files: map[string]string{"content/global/claude/CLAUDE.md": "# Rules\n"}}
	return connectTo(t, fake, token, readOnly), fake
}

// connectTo starts an MCP server whose Aegis is h, and returns a connected client.
func connectTo(t *testing.T, h http.Handler, token string, readOnly bool) *mcp.ClientSession {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	server := mcp.NewServer(&mcp.Implementation{Name: "aegis", Version: "test"}, &mcp.ServerOptions{Instructions: Instructions})
	Register(server, aegis.New(srv.URL, token, "test"), Options{ReadOnly: readOnly})

	st, ct := mcp.NewInMemoryTransports()
	ctx := context.Background()
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (map[string]any, string, bool) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: protocol error %v", name, err)
	}
	var text string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text += tc.Text
		}
	}
	out, _ := res.StructuredContent.(map[string]any)
	if out == nil && res.StructuredContent != nil {
		raw, _ := json.Marshal(res.StructuredContent)
		json.Unmarshal(raw, &out)
	}
	return out, text, res.IsError
}

func toolNames(t *testing.T, cs *mcp.ClientSession) []string {
	t.Helper()
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tl := range res.Tools {
		names = append(names, tl.Name)
	}
	sort.Strings(names)
	return names
}

func TestReadOnlyHidesWriteTools(t *testing.T) {
	ro, _ := connect(t, "good", true)
	rw, _ := connect(t, "good", false)
	for _, n := range toolNames(t, ro) {
		if strings.Contains(n, "create") || strings.Contains(n, "update") || strings.Contains(n, "delete") ||
			strings.Contains(n, "repo_") && n != "aegis_list_repos" || strings.Contains(n, "move") {
			t.Errorf("read-only server offers %s", n)
		}
	}
	all := strings.Join(toolNames(t, rw), " ")
	for _, want := range []string{"aegis_create_file", "aegis_update_file", "aegis_delete_file",
		"aegis_create_variable", "aegis_import_repo", "aegis_scan_repo_import", "aegis_list_activity"} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %s in %s", want, all)
		}
	}
}

func TestReadUpdateDeleteFlow(t *testing.T) {
	cs, fake := connect(t, "good", false)
	path := "global/claude/CLAUDE.md"

	got, text, isErr := call(t, cs, "aegis_read_file", map[string]any{"plane": "content", "path": path})
	if isErr {
		t.Fatalf("read: %s", text)
	}
	tag, _ := got["etag"].(string)
	if got["body"] != "# Rules\n" || tag == "" {
		t.Fatalf("read returned %v", got)
	}

	// A stale etag is refused, and the error tells the model what to do.
	_, text, isErr = call(t, cs, "aegis_update_file", map[string]any{
		"plane": "content", "path": path, "body": "x", "etag": `"stale"`,
	})
	if !isErr || !strings.Contains(text, "read it again") {
		t.Fatalf("stale update: isErr=%v %s", isErr, text)
	}

	got, text, isErr = call(t, cs, "aegis_update_file", map[string]any{
		"plane": "content", "path": path, "body": "# Rules v2\n", "etag": tag,
	})
	if isErr || fake.files["content/"+path] != "# Rules v2\n" {
		t.Fatalf("update: %s %v", text, got)
	}
	newTag, _ := got["etag"].(string)

	_, text, isErr = call(t, cs, "aegis_delete_file", map[string]any{"plane": "content", "path": path, "etag": newTag})
	if isErr {
		t.Fatalf("delete: %s", text)
	}
	if _, ok := fake.files["content/"+path]; ok {
		t.Fatal("file still present")
	}
}

func TestCreateRefusesExisting(t *testing.T) {
	cs, _ := connect(t, "good", false)
	_, text, isErr := call(t, cs, "aegis_create_file", map[string]any{
		"plane": "content", "path": "global/claude/CLAUDE.md", "body": "x",
	})
	if !isErr || !strings.Contains(text, "aegis_update_file") {
		t.Fatalf("isErr=%v %s", isErr, text)
	}
	_, text, isErr = call(t, cs, "aegis_create_file", map[string]any{
		"plane": "config", "path": "roles/be/claude/settings.json", "body": "{}",
	})
	if isErr {
		t.Fatalf("config create: %s", text)
	}
}

func TestBadInputs(t *testing.T) {
	cs, _ := connect(t, "good", false)
	_, _, isErr := call(t, cs, "aegis_list_files", map[string]any{"plane": "secrets"})
	if !isErr {
		t.Fatal("unknown plane accepted")
	}
	_, text, isErr := call(t, cs, "aegis_read_file", map[string]any{"plane": "content", "path": "../../etc/passwd"})
	if !isErr || !strings.Contains(text, "..") {
		t.Fatalf("traversal: isErr=%v %s", isErr, text)
	}
}

func TestRejectedToken(t *testing.T) {
	cs, _ := connect(t, "bad", false)
	_, text, isErr := call(t, cs, "aegis_whoami", nil)
	if !isErr || !strings.Contains(text, "401") || !strings.Contains(text, "aegis-mcp setup") {
		t.Fatalf("isErr=%v %s", isErr, text)
	}
}
