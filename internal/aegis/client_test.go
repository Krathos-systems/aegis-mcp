package aegis

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

var ctx = context.Background()

// serve starts a test server running h and returns a client for it.
func serve(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return New(srv.URL+"/", "tok", "aegis-mcp/test")
}

// reply answers every request with status and body.
func reply(status int, body string, headers ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		for i := 0; i+1 < len(headers); i += 2 {
			w.Header().Set(headers[i], headers[i+1])
		}
		w.WriteHeader(status)
		io.WriteString(w, body)
	}
}

// closedServer returns a client for an address nothing listens on.
func closedServer(t *testing.T) *Client {
	t.Helper()
	srv := httptest.NewServer(http.NotFoundHandler())
	u := srv.URL
	srv.Close()
	return New(u, "tok", "")
}

func statusOf(err error) int {
	var se *StatusError
	if errors.As(err, &se) {
		return se.Status
	}
	return 0
}

// ---- paths and planes ----

func TestCleanPath(t *testing.T) {
	ok := map[string]string{
		"global/claude/CLAUDE.md":             "global/claude/CLAUDE.md",
		"roles/back end/claude/settings.json": "roles/back%20end/claude/settings.json",
		"repos/a/claude/skills/x/run?.sh":     "repos/a/claude/skills/x/run%3F.sh",
	}
	for in, want := range ok {
		got, err := CleanPath(in)
		if err != nil || got != want {
			t.Errorf("CleanPath(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "/etc/passwd", "a/../b", "../x", "a//b", "./a", `a\b`, "a/"} {
		if _, err := CleanPath(bad); err == nil {
			t.Errorf("CleanPath(%q) accepted", bad)
		}
	}
}

func TestParsePlane(t *testing.T) {
	for _, ok := range []string{"content", "config"} {
		if p, err := ParsePlane(ok); err != nil || string(p) != ok {
			t.Errorf("ParsePlane(%q) = %q, %v", ok, p, err)
		}
	}
	for _, bad := range []string{"", "Content", "secrets"} {
		if _, err := ParsePlane(bad); err == nil {
			t.Errorf("ParsePlane(%q) accepted", bad)
		}
	}
}

func TestContentType(t *testing.T) {
	cases := map[string]string{
		"a/settings.json": "application/json; charset=utf-8",
		"a/CLAUDE.md":     "text/markdown; charset=utf-8",
		"a/run.sh":        "text/plain; charset=utf-8",
	}
	for in, want := range cases {
		if got := contentType(in); got != want {
			t.Errorf("contentType(%q) = %q", in, got)
		}
	}
}

// ---- transport ----

func TestDoSendsHeaders(t *testing.T) {
	var got *http.Request
	var body string
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		got = r
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		w.Header().Set("ETag", `"e1"`)
		w.WriteHeader(http.StatusOK)
	})
	resp, err := c.Do(ctx, Request{
		Method: http.MethodPut, Path: "/x", Query: url.Values{"q": {"1"}},
		Body: []byte("hi"), ContentType: "text/plain", IfMatch: `"e0"`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.URL.Path != "/api/x" || got.URL.RawQuery != "q=1" {
		t.Errorf("url = %s", got.URL)
	}
	h := got.Header
	if h.Get("Authorization") != "Bearer tok" || h.Get("User-Agent") != "aegis-mcp/test" ||
		h.Get("Content-Type") != "text/plain" || h.Get("If-Match") != `"e0"` || body != "hi" {
		t.Errorf("headers %v body %q", h, body)
	}
	if resp.ETag != `"e1"` || resp.Status != 200 || c.BaseURL() == "" || strings.HasSuffix(c.BaseURL(), "/") {
		t.Errorf("resp %+v base %q", resp, c.BaseURL())
	}
}

func TestDoWithoutOptionalHeaders(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got = r.Header }))
	defer srv.Close()
	if _, err := New(srv.URL, "tok", "").Do(ctx, Request{Method: http.MethodGet, Path: "/x"}); err != nil {
		t.Fatal(err)
	}
	if got.Get("If-Match") != "" || got.Get("Content-Type") != "" || got.Get("User-Agent") != "Go-http-client/1.1" {
		t.Errorf("unexpected headers %v", got)
	}
}

func TestDoErrors(t *testing.T) {
	t.Run("bad method", func(t *testing.T) {
		if _, err := New("http://127.0.0.1", "tok", "").Do(ctx, Request{Method: "BAD METHOD", Path: "/"}); err == nil {
			t.Fatal("accepted")
		}
	})
	t.Run("unreachable", func(t *testing.T) {
		_, err := closedServer(t).Do(ctx, Request{Method: http.MethodGet, Path: "/x?secret=1"})
		if err == nil || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "tok") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("truncated body", func(t *testing.T) {
		c := serve(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", "100")
			w.WriteHeader(http.StatusOK)
			io.WriteString(w, "short")
			conn, _, _ := w.(http.Hijacker).Hijack()
			conn.Close()
		})
		if _, err := c.Do(ctx, Request{Method: http.MethodGet, Path: "/x"}); err == nil {
			t.Fatal("truncated body accepted")
		}
	})
	t.Run("status with JSON error", func(t *testing.T) {
		c := serve(t, reply(403, `{"error":"forbidden","required":"editor","held":"reader"}`))
		_, err := c.Repos(ctx)
		var se *StatusError
		if !errors.As(err, &se) || se.Status != 403 || se.Message != "forbidden" || se.Body["required"] != "editor" {
			t.Fatalf("err = %#v", err)
		}
		if err.Error() != "GET /repos: 403 forbidden" {
			t.Errorf("Error() = %q", err.Error())
		}
	})
	t.Run("status without a message", func(t *testing.T) {
		c := serve(t, reply(502, `<html>bad gateway</html>`))
		_, err := c.Me(ctx)
		var se *StatusError
		if !errors.As(err, &se) || se.Message != "" || se.Body != nil {
			t.Fatalf("err = %#v", err)
		}
		if err.Error() != "GET /admin/me: 502 Bad Gateway" {
			t.Errorf("Error() = %q", err.Error())
		}
	})
}

func TestRedirectIsNotFollowed(t *testing.T) {
	var followed bool
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { followed = true }))
	defer target.Close()
	c := serve(t, func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) })
	if _, err := c.Me(ctx); statusOf(err) != http.StatusFound || followed {
		t.Fatalf("redirect followed=%v err=%v", followed, err)
	}
}

func TestJSON(t *testing.T) {
	t.Run("not JSON", func(t *testing.T) {
		if _, err := serve(t, reply(200, "<html>")).Me(ctx); err == nil || !strings.Contains(err.Error(), "not JSON") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("empty body", func(t *testing.T) {
		v, err := serve(t, reply(200, "")).Variables(ctx)
		if err != nil || v != nil {
			t.Fatalf("v=%v err=%v", v, err)
		}
	})
	t.Run("nil out", func(t *testing.T) {
		if _, err := serve(t, reply(200, "{}")).JSON(ctx, Request{Method: http.MethodGet, Path: "/x"}, nil); err != nil {
			t.Fatal(err)
		}
	})
}

func TestScrub(t *testing.T) {
	plain := errors.New("boom")
	if scrub(plain, "https://a") != plain {
		t.Error("a non-URL error was changed")
	}
	ue := &url.Error{Op: "Get", URL: "https://a/api/x?k=secret", Err: &net.OpError{Op: "dial", Err: errors.New("refused")}}
	got := scrub(ue, "https://a").Error()
	if strings.Contains(got, "secret") || !strings.Contains(got, "refused") {
		t.Errorf("scrub = %q", got)
	}
}

// ---- health ----

func TestHealth(t *testing.T) {
	var auth string
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		if r.URL.Path != "/health" {
			t.Errorf("path %s", r.URL.Path)
		}
		io.WriteString(w, `{"status":"ok","versions":{"api":"v2"}}`)
	})
	h, err := c.Health(ctx)
	if err != nil || h["status"] != "ok" {
		t.Fatalf("h=%v err=%v", h, err)
	}
	if auth != "" {
		t.Error("health sent the token")
	}
}

func TestHealthErrors(t *testing.T) {
	if _, err := New("http://a\x7f", "tok", "").Health(ctx); err == nil {
		t.Error("bad base URL accepted")
	}
	if _, err := closedServer(t).Health(ctx); err == nil {
		t.Error("unreachable accepted")
	}
	if _, err := serve(t, reply(503, `{"error":"down"}`)).Health(ctx); statusOf(err) != 503 {
		t.Errorf("503: %v", err)
	}
	if _, err := serve(t, reply(200, "<html>captive portal</html>")).Health(ctx); err == nil ||
		!strings.Contains(err.Error(), "Aegis") {
		t.Errorf("non-JSON: %v", err)
	}
}

// ---- admin endpoints ----

func TestAdminEndpoints(t *testing.T) {
	var last *http.Request
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		last = r
		switch r.URL.Path {
		case "/api/repos":
			io.WriteString(w, `[{"slug":"web"}]`)
		default:
			io.WriteString(w, `{"ok":true}`)
		}
	})
	if v, err := c.Me(ctx); err != nil || v["ok"] != true || last.URL.Path != "/api/admin/me" {
		t.Errorf("Me: %v %v", v, err)
	}
	if v, err := c.Repos(ctx); err != nil || len(v.([]any)) != 1 {
		t.Errorf("Repos: %v %v", v, err)
	}
	if v, err := c.Variables(ctx); err != nil || v["ok"] != true || last.URL.Path != "/api/variables" {
		t.Errorf("Variables: %v %v", v, err)
	}
	v, err := c.PreviewRole(ctx, "be", "web app")
	if err != nil || v["ok"] != true || last.URL.Path != "/api/admin/preview/role" ||
		last.URL.Query().Get("repo") != "web app" || last.URL.Query().Get("role") != "be" {
		t.Errorf("PreviewRole: %v %v %s", v, err, last.URL)
	}
}

// ---- files ----

func TestListFiles(t *testing.T) {
	var path string
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		io.WriteString(w, `["a.md","b.md"]`)
	})
	files, err := c.ListFiles(ctx, Config)
	if err != nil || len(files) != 2 || path != "/api/config/files" {
		t.Fatalf("files=%v err=%v path=%s", files, err, path)
	}
}

func TestReadFile(t *testing.T) {
	var path string
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.EscapedPath()
		w.Header().Set("ETag", `"e"`)
		io.WriteString(w, "# hi {{ aegis.X }}")
	})
	f, err := c.ReadFile(ctx, Content, "teams/a b/claude/CLAUDE.md")
	if err != nil || f.Body != "# hi {{ aegis.X }}" || f.ETag != `"e"` {
		t.Fatalf("f=%+v err=%v", f, err)
	}
	if path != "/api/content/files/teams/a%20b/claude/CLAUDE.md" {
		t.Errorf("path = %s", path)
	}
	if _, err := c.ReadFile(ctx, Content, "../x"); err == nil {
		t.Error("bad path accepted")
	}
	if _, err := serve(t, reply(404, `{"error":"file not found"}`)).ReadFile(ctx, Content, "x.md"); statusOf(err) != 404 {
		t.Errorf("404: %v", err)
	}
}

func TestCreateContent(t *testing.T) {
	var method, ctype string
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		method, ctype = r.Method, r.Header.Get("Content-Type")
		w.Header().Set("ETag", `"new"`)
		w.WriteHeader(http.StatusCreated)
	})
	r, err := c.CreateFile(ctx, Content, "a/CLAUDE.md", "x")
	if err != nil || !r.Created || r.ETag != `"new"` || method != http.MethodPost || !strings.HasPrefix(ctype, "text/markdown") {
		t.Fatalf("r=%+v err=%v method=%s ctype=%s", r, err, method, ctype)
	}

	if _, err := c.CreateFile(ctx, Content, "/abs", "x"); err == nil {
		t.Error("bad path accepted")
	}
	_, err = serve(t, reply(409, `{"error":"file already exists"}`)).CreateFile(ctx, Content, "a.md", "x")
	if !errors.Is(err, ErrExists) {
		t.Errorf("409: %v", err)
	}
	_, err = serve(t, reply(403, `{"error":"forbidden"}`)).CreateFile(ctx, Content, "a.md", "x")
	if statusOf(err) != 403 {
		t.Errorf("403: %v", err)
	}
}

func TestCreateConfig(t *testing.T) {
	t.Run("new file", func(t *testing.T) {
		var methods []string
		c := serve(t, func(w http.ResponseWriter, r *http.Request) {
			methods = append(methods, r.Method)
			if r.Method == http.MethodGet {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("ETag", `"new"`)
			w.WriteHeader(http.StatusCreated)
		})
		r, err := c.CreateFile(ctx, Config, "roles/be/claude/settings.json", "{}")
		if err != nil || !r.Created || r.ETag != `"new"` || strings.Join(methods, ",") != "GET,PUT" {
			t.Fatalf("r=%+v err=%v methods=%v", r, err, methods)
		}
	})
	t.Run("already there", func(t *testing.T) {
		var puts int
		c := serve(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPut {
				puts++
			}
			io.WriteString(w, "{}")
		})
		_, err := c.CreateFile(ctx, Config, "global/claude/settings.json", "{}")
		if !errors.Is(err, ErrExists) || puts != 0 {
			t.Fatalf("err=%v puts=%d", err, puts)
		}
	})
	t.Run("existence check fails", func(t *testing.T) {
		_, err := serve(t, reply(500, `{"error":"boom"}`)).CreateFile(ctx, Config, "a.json", "{}")
		if statusOf(err) != 500 {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("put fails", func(t *testing.T) {
		c := serve(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, `{"error":"invalid JSON"}`)
		})
		_, err := c.CreateFile(ctx, Config, "a.json", "{")
		if statusOf(err) != 400 {
			t.Fatalf("err=%v", err)
		}
	})
}

func TestUpdateFile(t *testing.T) {
	var ifMatch string
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		ifMatch = r.Header.Get("If-Match")
		w.Header().Set("ETag", `"e2"`)
	})
	r, err := c.UpdateFile(ctx, Config, "a.json", "{}", `"e1"`)
	if err != nil || r.Created || r.ETag != `"e2"` || ifMatch != `"e1"` {
		t.Fatalf("r=%+v err=%v ifMatch=%s", r, err, ifMatch)
	}
	if _, err := c.UpdateFile(ctx, Content, "a.md", "x", ""); err == nil {
		t.Error("missing etag accepted")
	}
	if _, err := c.UpdateFile(ctx, Content, "a/../b", "x", `"e"`); err == nil {
		t.Error("bad path accepted")
	}
	if _, err := serve(t, reply(409, `{"error":"changed"}`)).UpdateFile(ctx, Content, "a.md", "x", `"e"`); statusOf(err) != 409 {
		t.Errorf("409: %v", err)
	}
}

func TestDeleteFile(t *testing.T) {
	var method, ifMatch string
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		method, ifMatch = r.Method, r.Header.Get("If-Match")
		w.WriteHeader(http.StatusNoContent)
	})
	if err := c.DeleteFile(ctx, Content, "a.md", `"e"`); err != nil || method != http.MethodDelete || ifMatch != `"e"` {
		t.Fatalf("err=%v method=%s ifMatch=%s", err, method, ifMatch)
	}
	if err := c.DeleteFile(ctx, Config, "a.json", ""); err == nil {
		t.Error("missing etag accepted")
	}
	if err := c.DeleteFile(ctx, Config, "", `"e"`); err == nil {
		t.Error("empty path accepted")
	}
	if err := serve(t, reply(404, `{}`)).DeleteFile(ctx, Content, "a.md", `"e"`); statusOf(err) != 404 {
		t.Errorf("404: %v", err)
	}
}
