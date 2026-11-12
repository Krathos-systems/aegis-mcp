package aegis

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// recorded is one request a test server saw.
type recorded struct {
	Method, Path, Query string
	Body                map[string]any
}

// recorder answers every request with status and body and keeps the last one.
func recorder(t *testing.T, status int, body string) (*Client, *recorded) {
	t.Helper()
	last := &recorded{}
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		*last = recorded{Method: r.Method, Path: r.URL.EscapedPath(), Query: r.URL.RawQuery}
		data, _ := io.ReadAll(r.Body)
		if len(data) > 0 {
			if err := json.Unmarshal(data, &last.Body); err != nil {
				t.Errorf("request body is not JSON: %q", data)
			}
			if ct := r.Header.Get("Content-Type"); ct != "application/json" {
				t.Errorf("Content-Type = %q", ct)
			}
		}
		w.WriteHeader(status)
		io.WriteString(w, body)
	})
	return c, last
}

func str(s string) *string { return &s }

func TestActivity(t *testing.T) {
	c, last := recorder(t, 200, `{"commits":[]}`)
	if _, err := c.Activity(ctx, 0, 0); err != nil || last.Path != "/api/activity" || last.Query != "" {
		t.Errorf("defaults: %+v %v", last, err)
	}
	if _, err := c.Activity(ctx, 10, 20); err != nil || last.Query != "limit=10&offset=20" {
		t.Errorf("paged: %+v %v", last, err)
	}
}

func TestChange(t *testing.T) {
	c, last := recorder(t, 200, `{"sha":"abc1234","diff":""}`)
	if _, err := c.Change(ctx, "abc1234"); err != nil || last.Path != "/api/activity/abc1234" {
		t.Errorf("%+v %v", last, err)
	}
	for _, bad := range []string{"", "abc", "HEAD~1", "--all", strings.Repeat("a", 41)} {
		if _, err := c.Change(ctx, bad); err == nil {
			t.Errorf("Change(%q) accepted", bad)
		}
	}
}

func TestCreateVariable(t *testing.T) {
	c, last := recorder(t, 201, `{"name":"API_URL"}`)
	v, err := c.CreateVariable(ctx, "API_URL", VariableChange{Kind: str("cleartext"), Value: str("https://x"), Description: str("d")})
	if err != nil || v["name"] != "API_URL" {
		t.Fatalf("%v %v", v, err)
	}
	if last.Method != http.MethodPost || last.Path != "/api/variables" || last.Body["name"] != "API_URL" ||
		last.Body["kind"] != "cleartext" || last.Body["value"] != "https://x" || last.Body["description"] != "d" {
		t.Errorf("%+v", last)
	}
}

func TestUpdateVariableSendsOnlyWhatIsGiven(t *testing.T) {
	c, last := recorder(t, 200, `{}`)
	if _, err := c.UpdateVariable(ctx, "A B", VariableChange{Description: str("new")}); err != nil {
		t.Fatal(err)
	}
	if last.Method != http.MethodPut || last.Path != "/api/variables/A%20B" {
		t.Errorf("%+v", last)
	}
	if _, hasValue := last.Body["value"]; hasValue || last.Body["description"] != "new" {
		t.Errorf("body = %v", last.Body)
	}
	if _, hasKind := last.Body["kind"]; hasKind {
		t.Errorf("body = %v", last.Body)
	}
}

func TestDeleteVariable(t *testing.T) {
	c, last := recorder(t, 204, ``)
	if err := c.DeleteVariable(ctx, "OLD", false); err != nil || last.Method != http.MethodDelete || last.Query != "" {
		t.Errorf("%+v %v", last, err)
	}
	if err := c.DeleteVariable(ctx, "OLD", true); err != nil || last.Query != "force=1" {
		t.Errorf("force: %+v %v", last, err)
	}
}

func TestVariable(t *testing.T) {
	c, _ := recorder(t, 200, `{"variables":["junk",{"name":"A","description":"first"},{"name":"B"}]}`)
	v, err := c.Variable(ctx, "A")
	if err != nil || v["description"] != "first" {
		t.Errorf("found: %v %v", v, err)
	}
	if _, err := c.Variable(ctx, "MISSING"); statusOf(err) != 404 || !strings.Contains(err.Error(), "MISSING") {
		t.Errorf("missing: %v", err)
	}
	failing, _ := recorder(t, 403, `{"error":"forbidden"}`)
	if _, err := failing.Variable(ctx, "A"); statusOf(err) != 403 {
		t.Errorf("listing fails: %v", err)
	}
}

func TestRepoCalls(t *testing.T) {
	c, last := recorder(t, 200, `{"id":7}`)

	if _, err := c.RegisterRepo(ctx, "https://example.com/web.git", "web"); err != nil ||
		last.Method != http.MethodPost || last.Path != "/api/repos" ||
		last.Body["remote_url"] != "https://example.com/web.git" || last.Body["repo"] != "web" {
		t.Errorf("register: %+v %v", last, err)
	}
	if _, err := c.MoveRepo(ctx, 7, "site"); err != nil ||
		last.Method != http.MethodPatch || last.Path != "/api/repos/7" || last.Body["repo"] != "site" {
		t.Errorf("move: %+v %v", last, err)
	}
	if err := c.UnregisterRepo(ctx, 7); err != nil || last.Method != http.MethodDelete || last.Path != "/api/repos/7" {
		t.Errorf("unregister: %+v %v", last, err)
	}
	if _, err := c.ScanImport(ctx, 7); err != nil || last.Method != http.MethodPost || last.Path != "/api/repos/7/import/scan" {
		t.Errorf("scan: %+v %v", last, err)
	}
}

func TestImport(t *testing.T) {
	c, last := recorder(t, 200, `{"written":[]}`)
	sha := strings.Repeat("ab", 20)

	if _, err := c.Import(ctx, 7, sha, nil); err != nil || last.Path != "/api/repos/7/import" {
		t.Fatalf("%+v %v", last, err)
	}
	if replace, ok := last.Body["replace"].([]any); !ok || len(replace) != 0 || last.Body["sha"] != sha {
		t.Errorf("nil replace must be sent as []: %v", last.Body)
	}
	if _, err := c.Import(ctx, 7, sha, []string{"repos/web/claude/CLAUDE.md"}); err != nil ||
		len(last.Body["replace"].([]any)) != 1 {
		t.Errorf("%+v %v", last, err)
	}
	if _, err := c.Import(ctx, 7, "abc", nil); err == nil {
		t.Error("short sha accepted")
	}
}

func TestSendJSONRefusesWhatCannotBeEncoded(t *testing.T) {
	c, _ := recorder(t, 200, `{}`)
	if _, err := c.SendJSON(ctx, Request{Method: http.MethodPost, Path: "/x"}, make(chan int), nil); err == nil {
		t.Error("a channel was encoded")
	}
}

func TestRequestTimeout(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(2 * time.Second):
		case <-r.Context().Done():
		}
	})
	start := time.Now()
	_, err := c.Do(ctx, Request{Method: http.MethodGet, Path: "/slow", Timeout: 50 * time.Millisecond})
	if err == nil || time.Since(start) > time.Second {
		t.Fatalf("err=%v after %s", err, time.Since(start))
	}
}

func TestIsHex(t *testing.T) {
	if !isHex("deadBEEF09", 4, 40) || isHex("xyz1", 1, 40) || isHex("ab", 4, 40) || isHex("abcde", 1, 4) {
		t.Error("isHex disagrees")
	}
}
