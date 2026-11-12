package tools

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// recordingAegis answers like routes and keeps every request it saw.
type recordingAegis struct {
	routes
	mu   sync.Mutex
	seen []seenRequest
}

type seenRequest struct {
	key, query string
	body       map[string]any
}

func (a *recordingAegis) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	data, _ := io.ReadAll(r.Body)
	var body map[string]any
	json.Unmarshal(data, &body)
	a.mu.Lock()
	a.seen = append(a.seen, seenRequest{key: r.Method + " " + r.URL.EscapedPath(), query: r.URL.RawQuery, body: body})
	a.mu.Unlock()
	a.routes.ServeHTTP(w, r)
}

func (a *recordingAegis) last(key string) seenRequest {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := len(a.seen) - 1; i >= 0; i-- {
		if a.seen[i].key == key {
			return a.seen[i]
		}
	}
	return seenRequest{}
}

func TestActivityTools(t *testing.T) {
	cs := connectTo(t, routes{
		"GET /api/activity":         {200, `{"commits":[{"sha":"abc1234","author":"ada"}],"ahead":0}`},
		"GET /api/activity/abc1234": {200, `{"sha":"abc1234","diff":"+hello"}`},
	}, "x", true) // reading the history works in read-only mode
	out, text, isErr := call(t, cs, "aegis_list_activity", map[string]any{"limit": 5})
	if isErr || len(out["commits"].([]any)) != 1 {
		t.Fatalf("list: %s %v", text, out)
	}
	out, text, isErr = call(t, cs, "aegis_show_change", map[string]any{"sha": "abc1234"})
	if isErr || out["diff"] != "+hello" {
		t.Fatalf("show: %s %v", text, out)
	}
}

func TestVariableTools(t *testing.T) {
	a := &recordingAegis{routes: routes{
		"GET /api/variables":         {200, `{"variables":[{"name":"API_URL","kind":"cleartext","description":"keep me"}]}`},
		"POST /api/variables":        {201, `{"name":"NEW"}`},
		"PUT /api/variables/API_URL": {200, `{"name":"API_URL"}`},
		"DELETE /api/variables/OLD":  {204, ``},
	}}
	cs := connectTo(t, a, "x", false)

	if _, text, isErr := call(t, cs, "aegis_create_variable", map[string]any{"name": "NEW", "kind": "secret", "value": "s3"}); isErr {
		t.Fatal(text)
	}
	if b := a.last("POST /api/variables").body; b["kind"] != "secret" || b["value"] != "s3" {
		t.Errorf("create body %v", b)
	}

	// No description given: the current one is sent back, or Aegis would clear it.
	if _, text, isErr := call(t, cs, "aegis_update_variable", map[string]any{"name": "API_URL", "value": "https://new"}); isErr {
		t.Fatal(text)
	}
	if b := a.last("PUT /api/variables/API_URL").body; b["description"] != "keep me" || b["value"] != "https://new" {
		t.Errorf("update body %v", b)
	}
	// An explicit empty description clears it, and no value keeps the stored one.
	if _, text, isErr := call(t, cs, "aegis_update_variable", map[string]any{"name": "API_URL", "description": ""}); isErr {
		t.Fatal(text)
	}
	if b := a.last("PUT /api/variables/API_URL").body; b["description"] != "" || b["value"] != nil {
		t.Errorf("update body %v", b)
	}
	// Updating a variable that does not exist fails before any PUT.
	if _, text, isErr := call(t, cs, "aegis_update_variable", map[string]any{"name": "NOPE", "value": "x"}); !isErr || !strings.Contains(text, "NOPE") {
		t.Errorf("missing: %s", text)
	}

	out, text, isErr := call(t, cs, "aegis_delete_variable", map[string]any{"name": "OLD", "force": true})
	if isErr || out["done"] != true || a.last("DELETE /api/variables/OLD").query != "force=1" {
		t.Errorf("delete: %s %v", text, out)
	}
}

func TestRepoTools(t *testing.T) {
	sha := strings.Repeat("ab", 20)
	a := &recordingAegis{routes: routes{
		"POST /api/repos":               {201, `{"id":3,"repo":"web"}`},
		"PATCH /api/repos/3":            {200, `{"id":3,"repo":"site"}`},
		"DELETE /api/repos/3":           {204, ``},
		"POST /api/repos/3/import/scan": {200, `{"sha":"` + sha + `","files":[{"target":"repos/web/claude/CLAUDE.md","status":"new","problem":null}]}`},
		"POST /api/repos/3/import":      {200, `{"sha":"` + sha + `","written":["repos/web/claude/CLAUDE.md"],"skipped":[],"not_imported":[]}`},
	}}
	cs := connectTo(t, a, "x", false)

	steps := []struct {
		tool string
		args map[string]any
		key  string
	}{
		{"aegis_register_repo", map[string]any{"remote_url": "git@example.com:acme/web.git", "repo": "web"}, "POST /api/repos"},
		{"aegis_move_repo", map[string]any{"id": 3, "repo": "site"}, "PATCH /api/repos/3"},
		{"aegis_scan_repo_import", map[string]any{"id": 3}, "POST /api/repos/3/import/scan"},
		{"aegis_import_repo", map[string]any{"id": 3, "sha": sha, "replace": []string{"repos/web/claude/CLAUDE.md"}}, "POST /api/repos/3/import"},
		{"aegis_unregister_repo", map[string]any{"id": 3}, "DELETE /api/repos/3"},
	}
	for _, s := range steps {
		if _, text, isErr := call(t, cs, s.tool, s.args); isErr {
			t.Fatalf("%s: %s", s.tool, text)
		}
		if a.last(s.key).key == "" {
			t.Errorf("%s did not call %s", s.tool, s.key)
		}
	}
	if b := a.last("POST /api/repos").body; b["remote_url"] != "git@example.com:acme/web.git" {
		t.Errorf("register body %v", b)
	}
	if b := a.last("POST /api/repos/3/import").body; b["sha"] != sha || len(b["replace"].([]any)) != 1 {
		t.Errorf("import body %v", b)
	}
}

func TestWritesAllowed(t *testing.T) {
	cases := map[string]bool{"administrator": true, "editor": true, "collaborator": false, "reader": false, "": false}
	for level, want := range cases {
		if got := WritesAllowed(map[string]any{"highest_level": level}); got != want {
			t.Errorf("WritesAllowed(%q) = %v", level, got)
		}
	}
	if WritesAllowed(map[string]any{}) {
		t.Error("no level at all allows writes")
	}
}

func TestTokenExpiry(t *testing.T) {
	got, ok := TokenExpiry(map[string]any{"api_token": map[string]any{"expires_at": "2026-12-11T12:59:02Z"}})
	if !ok || got.Format("2006-01-02 15:04") != "2026-12-11 12:59" {
		t.Errorf("got %v %v", got, ok)
	}
	for _, me := range []map[string]any{
		{},
		{"api_token": nil},
		{"api_token": map[string]any{}},
		{"api_token": map[string]any{"expires_at": "next week"}},
	} {
		if _, ok := TokenExpiry(me); ok {
			t.Errorf("TokenExpiry(%v) reported an expiry", me)
		}
	}
}

func TestKindIsAnEnumInTheSchema(t *testing.T) {
	cs, _ := connect(t, "good", false)
	res, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range res.Tools {
		if tl.Name != "aegis_create_variable" && tl.Name != "aegis_update_variable" {
			continue
		}
		kind := tl.InputSchema.(map[string]any)["properties"].(map[string]any)["kind"].(map[string]any)
		if enum, _ := kind["enum"].([]any); len(enum) != 2 {
			t.Errorf("%s: kind enum = %v", tl.Name, kind["enum"])
		}
	}
}
