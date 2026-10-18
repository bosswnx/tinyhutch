package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type testEnv struct {
	t       *testing.T
	ts      *httptest.Server
	dir     string
	adminPW string
	client  *http.Client
	cm      *configManager
}

func newTestEnv(t *testing.T, rules []Rule) *testEnv {
	t.Helper()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "hello.txt"), "hello")
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(dir, "sub", "a.txt"), "aaa")

	configPath := filepath.Join(dir, "config.json")
	cfg := &Config{
		SiteName:       "TinyHutch",
		AdminPassword:  "adminpw",
		Rules:          rules,
		SessionHours:   12,
		MaxUploadMB:    10,
		InsecureCookie: true,
	}
	if err := saveConfig(configPath, cfg); err != nil {
		t.Fatal(err)
	}
	secretPath := filepath.Join(dir, ".hutch.secret")
	secret := []byte("0123456789abcdef0123456789abcdef")
	if err := os.WriteFile(secretPath, secret, 0o600); err != nil {
		t.Fatal(err)
	}
	cm := newConfigManager(configPath, secret)
	if _, err := cm.Load(); err != nil {
		t.Fatal(err)
	}
	files, err := newFileSystem(dir, configPath, secretPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { files.Close() })

	s := &server{
		cm:         cm,
		files:      files,
		limiter:    newLimiter(),
		version:    "test",
		rootDir:    dir,
		configPath: configPath,
		secretPath: secretPath,
		start:      time.Now(),
	}
	s.secret.Store(secret)
	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)

	return &testEnv{t: t, ts: ts, dir: dir, adminPW: "adminpw", client: newClient(), cm: cm}
}

func newClient() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar}
}

func writeTestFile(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (e *testEnv) do(c *http.Client, method, path, body string) (*http.Response, []byte) {
	e.t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, e.ts.URL+path, r)
	if err != nil {
		e.t.Fatal(err)
	}
	req.Header.Set("X-Requested-With", "hutch")
	if strings.HasPrefix(body, "{") || strings.HasPrefix(body, "[") {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, data
}

func (e *testEnv) anon(method, path, body string) (*http.Response, []byte) {
	return e.do(newClient(), method, path, body)
}

func (e *testEnv) unlock(c *http.Client, path, pw string) (*http.Response, []byte) {
	return e.do(c, "POST", "/api/unlock", fmt.Sprintf(`{"path":%q,"password":%q}`, path, pw))
}

func (e *testEnv) loginAdmin() {
	e.t.Helper()
	resp, body := e.unlock(e.client, "/", e.adminPW)
	if resp.StatusCode != http.StatusOK {
		e.t.Fatalf("admin login: %d %s", resp.StatusCode, body)
	}
}

func (e *testEnv) upload(c *http.Client, dir, name, content string, overwrite bool) (*http.Response, []byte) {
	q := url.Values{}
	q.Set("path", dir)
	q.Set("name", name)
	if overwrite {
		q.Set("overwrite", "1")
	}
	return e.do(c, "PUT", "/api/file?"+q.Encode(), content)
}

func (e *testEnv) setRules(rules ...Rule) {
	e.t.Helper()
	e.loginAdmin()
	payload, _ := json.Marshal(map[string]any{"rules": rules})
	resp, body := e.do(e.client, "PUT", "/api/admin/rules", string(payload))
	if resp.StatusCode != http.StatusOK {
		e.t.Fatalf("set rules: %d %s", resp.StatusCode, body)
	}
}

func TestAnonymousReadOnly(t *testing.T) {
	e := newTestEnv(t, nil)

	resp, body := e.anon("GET", "/api/list?path=/", "")
	if resp.StatusCode != 200 || !strings.Contains(string(body), "hello.txt") {
		t.Fatalf("list: %d %s", resp.StatusCode, body)
	}

	resp, body = e.anon("GET", "/api/download?path=/hello.txt", "")
	if resp.StatusCode != 200 || string(body) != "hello" {
		t.Fatalf("download: %d %q", resp.StatusCode, body)
	}

	req, _ := http.NewRequest("GET", e.ts.URL+"/api/download?path=/hello.txt", nil)
	req.Header.Set("Range", "bytes=0-1")
	r2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	rb, _ := io.ReadAll(r2.Body)
	r2.Body.Close()
	if r2.StatusCode != http.StatusPartialContent || string(rb) != "he" {
		t.Fatalf("range: %d %q", r2.StatusCode, rb)
	}

	for _, tc := range []struct{ method, path, body string }{
		{"PUT", "/api/file?path=/&name=x.txt", "x"},
		{"POST", "/api/mkdir", `{"path":"/","name":"x"}`},
		{"POST", "/api/rename", `{"path":"/hello.txt","name":"y.txt"}`},
		{"POST", "/api/delete", `{"path":"/hello.txt"}`},
	} {
		resp, _ := e.anon(tc.method, tc.path, tc.body)
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s %s: %d, want 403", tc.method, tc.path, resp.StatusCode)
		}
	}
}

func TestAdminWriteFlow(t *testing.T) {
	e := newTestEnv(t, nil)
	e.loginAdmin()

	resp, body := e.do(e.client, "POST", "/api/mkdir", `{"path":"/","name":"newdir"}`)
	if resp.StatusCode != 201 {
		t.Fatalf("mkdir: %d %s", resp.StatusCode, body)
	}
	resp, body = e.upload(e.client, "/newdir", "up.txt", "uploaded", false)
	if resp.StatusCode != 201 {
		t.Fatalf("upload: %d %s", resp.StatusCode, body)
	}
	resp, body = e.do(e.client, "GET", "/api/list?path=/newdir", "")
	if !strings.Contains(string(body), "up.txt") {
		t.Fatalf("list newdir: %s", body)
	}
	resp, body = e.do(e.client, "POST", "/api/rename", `{"path":"/newdir/up.txt","name":"renamed.txt"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("rename: %d %s", resp.StatusCode, body)
	}
	resp, body = e.do(e.client, "GET", "/api/download?path=/newdir/renamed.txt", "")
	if string(body) != "uploaded" {
		t.Fatalf("download renamed: %q", body)
	}
	resp, body = e.do(e.client, "POST", "/api/delete", `{"path":"/newdir/renamed.txt"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("delete: %d %s", resp.StatusCode, body)
	}
}

func TestPathLockAndReadOnlyPassword(t *testing.T) {
	e := newTestEnv(t, []Rule{{Path: "/private", Mode: modeLock, Password: "s3cret"}})
	writeTestFile(t, filepath.Join(e.dir, "private", "secret.txt"), "top secret")

	resp, _ := e.anon("GET", "/api/list?path=/private", "")
	if resp.StatusCode != 401 {
		t.Fatalf("anon list locked dir: %d", resp.StatusCode)
	}
	resp, _ = e.anon("GET", "/api/download?path=/private/secret.txt", "")
	if resp.StatusCode != 401 {
		t.Fatalf("anon download locked file: %d", resp.StatusCode)
	}

	c := newClient()
	resp, body := e.unlock(c, "/private", "s3cret")
	if resp.StatusCode != 200 {
		t.Fatalf("unlock: %d %s", resp.StatusCode, body)
	}
	resp, body = e.do(c, "GET", "/api/list?path=/private", "")
	if resp.StatusCode != 200 || !strings.Contains(string(body), "secret.txt") {
		t.Fatalf("list after unlock: %d %s", resp.StatusCode, body)
	}
	resp, body = e.do(c, "GET", "/api/download?path=/private/secret.txt", "")
	if resp.StatusCode != 200 || string(body) != "top secret" {
		t.Fatalf("download after unlock: %d %q", resp.StatusCode, body)
	}

	// A path password must never grant writes.
	resp, _ = e.do(c, "PUT", "/api/file?path=/private&name=x.txt", "x")
	if resp.StatusCode != 403 {
		t.Fatalf("write with path password: %d, want 403", resp.StatusCode)
	}
	resp, _ = e.do(c, "POST", "/api/delete", `{"path":"/private/secret.txt"}`)
	if resp.StatusCode != 403 {
		t.Fatalf("delete with path password: %d, want 403", resp.StatusCode)
	}
}

func TestRuleScopedUnlock(t *testing.T) {
	e := newTestEnv(t, []Rule{
		{Path: "/a", Mode: modeLock, Password: "same"},
		{Path: "/b", Mode: modeLock, Password: "same"},
	})
	writeTestFile(t, filepath.Join(e.dir, "a", "x.txt"), "a")
	writeTestFile(t, filepath.Join(e.dir, "b", "x.txt"), "b")

	c := newClient()
	if resp, body := e.unlock(c, "/a", "same"); resp.StatusCode != 200 {
		t.Fatalf("unlock /a: %d %s", resp.StatusCode, body)
	}
	if resp, _ := e.do(c, "GET", "/api/list?path=/a", ""); resp.StatusCode != 200 {
		t.Fatal("/a should be unlocked")
	}
	if resp, _ := e.do(c, "GET", "/api/list?path=/b", ""); resp.StatusCode != 401 {
		t.Fatal("/b must stay locked even with the same password")
	}
	// Wrong password must not create a session.
	c2 := newClient()
	if resp, _ := e.unlock(c2, "/a", "nope"); resp.StatusCode != 401 {
		t.Fatal("wrong password accepted")
	}
}

func TestDirectoryLockCoversSubtree(t *testing.T) {
	e := newTestEnv(t, []Rule{{Path: "/private", Mode: modeLock, Password: "p"}})
	writeTestFile(t, filepath.Join(e.dir, "private", "sub", "x.txt"), "inherited")

	// The whole subtree is closed to anonymous visitors.
	if resp, _ := e.anon("GET", "/api/list?path=/private", ""); resp.StatusCode != 401 {
		t.Fatal("directory should be locked")
	}
	if resp, _ := e.anon("GET", "/api/download?path=/private/sub/x.txt", ""); resp.StatusCode != 401 {
		t.Fatal("nested file should be locked by its parent directory")
	}

	// The admin sees the lock icon on every child, and the lock is inherited.
	e.loginAdmin()
	_, body := e.do(e.client, "GET", "/api/list?path=/private", "")
	var listing struct {
		Entries []listEntry `json:"entries"`
	}
	if err := json.Unmarshal(body, &listing); err != nil {
		t.Fatal(err)
	}
	if len(listing.Entries) != 1 {
		t.Fatalf("entries = %+v", listing.Entries)
	}
	child := listing.Entries[0]
	if !child.Protected || !child.Inherited || child.Locked {
		t.Fatalf("admin child lock flags = %+v", child)
	}

	// After unlocking the directory the subtree is readable, but the badge and
	// inherited flag stay so the per-file lock control remains disabled.
	reader := newClient()
	if resp, body := e.unlock(reader, "/private", "p"); resp.StatusCode != 200 {
		t.Fatalf("unlock: %d %s", resp.StatusCode, body)
	}
	_, body = e.do(reader, "GET", "/api/list?path=/private", "")
	if err := json.Unmarshal(body, &listing); err != nil {
		t.Fatal(err)
	}
	child = listing.Entries[0]
	if !child.Protected || !child.Inherited || child.Locked {
		t.Fatalf("unlocked child flags = %+v", child)
	}
	if resp, body := e.do(reader, "GET", "/api/list?path=/private/sub", ""); resp.StatusCode != 200 {
		t.Fatalf("nested list after unlock: %d %s", resp.StatusCode, body)
	}
	if resp, body := e.do(reader, "GET", "/api/download?path=/private/sub/x.txt", ""); resp.StatusCode != 200 || string(body) != "inherited" {
		t.Fatalf("nested download after unlock: %d %q", resp.StatusCode, body)
	}
}

func TestAdminRulesRejectOverlap(t *testing.T) {
	e := newTestEnv(t, []Rule{{Path: "/a", Mode: modeLock, Password: "p"}})
	e.loginAdmin()

	payload := `{"rules":[{"path":"/a","mode":"lock","password":"p"},{"path":"/a/b","password":"q"}]}`
	resp, body := e.do(e.client, "PUT", "/api/admin/rules", payload)
	if resp.StatusCode != 400 {
		t.Fatalf("overlap: %d %s", resp.StatusCode, body)
	}
	var out struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(body, &out)
	if out.Code != "rules_overlap" {
		t.Fatalf("code = %q, body = %s", out.Code, body)
	}

	// A duplicate path is also an overlap.
	payload = `{"rules":[{"path":"/a","password":"p"},{"path":"/a/","password":"q"}]}`
	if resp, body := e.do(e.client, "PUT", "/api/admin/rules", payload); resp.StatusCode != 400 {
		t.Fatalf("duplicate: %d %s", resp.StatusCode, body)
	}

	// Non-overlapping paths still save.
	e.setRules(
		Rule{Path: "/a", Mode: modeLock, Password: "p"},
		Rule{Path: "/b", Mode: modeLock, Password: "q"},
	)
}

func TestAdminPasswordChangeInvalidatesOthers(t *testing.T) {
	e := newTestEnv(t, nil)
	e.loginAdmin()
	other := newClient()
	if resp, body := e.unlock(other, "/", "adminpw"); resp.StatusCode != 200 {
		t.Fatalf("other login: %d %s", resp.StatusCode, body)
	}
	if resp, _ := e.do(other, "GET", "/api/admin/config", ""); resp.StatusCode != 200 {
		t.Fatal("other admin should work before change")
	}

	resp, body := e.do(e.client, "POST", "/api/admin/password",
		`{"current_password":"adminpw","new_password":"newadminpw"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("change password: %d %s", resp.StatusCode, body)
	}
	if resp, _ := e.do(other, "GET", "/api/admin/config", ""); resp.StatusCode != 403 {
		t.Fatal("other admin session should be invalid")
	}
	if resp, _ := e.unlock(other, "/", "adminpw"); resp.StatusCode != 401 {
		t.Fatal("old password still works")
	}
	if resp, body := e.unlock(other, "/", "newadminpw"); resp.StatusCode != 200 {
		t.Fatalf("new password: %d %s", resp.StatusCode, body)
	}
	if resp, _ := e.do(e.client, "GET", "/api/admin/config", ""); resp.StatusCode != 200 {
		t.Fatal("current session should stay admin")
	}
}

func TestEmptyAdminPassword(t *testing.T) {
	e := newTestEnv(t, nil)
	if _, err := e.cm.Update(func(c *Config) error {
		c.AdminPassword = ""
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// Anonymous visitors are still read-only until they click 登录.
	if resp, _ := e.anon("PUT", "/api/file?path=/&name=x.txt", "x"); resp.StatusCode != 403 {
		t.Fatalf("anon write: %d, want 403", resp.StatusCode)
	}

	c := newClient()
	resp, body := e.unlock(c, "/", "")
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"admin":true`) {
		t.Fatalf("empty password login: %d %s", resp.StatusCode, body)
	}
	if resp, body := e.upload(c, "/", "x.txt", "x", false); resp.StatusCode != 201 {
		t.Fatalf("write after login: %d %s", resp.StatusCode, body)
	}

	// An arbitrary non-empty password must not work in this mode.
	c2 := newClient()
	if resp, _ := e.unlock(c2, "/", "whatever"); resp.StatusCode == 200 {
		t.Fatal("non-empty password should not authenticate when admin_password is empty")
	}
}

func TestDeleteAdminPassword(t *testing.T) {
	e := newTestEnv(t, nil)
	e.loginAdmin()

	resp, body := e.do(e.client, "POST", "/api/admin/password", `{"current_password":"adminpw","new_password":""}`)
	if resp.StatusCode != 200 {
		t.Fatalf("delete password: %d %s", resp.StatusCode, body)
	}
	// The current session must stay admin after deletion.
	if resp, _ := e.do(e.client, "GET", "/api/admin/config", ""); resp.StatusCode != 200 {
		t.Fatal("session lost admin after deleting the password")
	}
	// Empty password login now works.
	if resp, body := e.unlock(newClient(), "/", ""); resp.StatusCode != 200 {
		t.Fatalf("empty login: %d %s", resp.StatusCode, body)
	}
	// The old password no longer works.
	if resp, _ := e.unlock(newClient(), "/", "adminpw"); resp.StatusCode != 401 {
		t.Fatal("old password still works after deletion")
	}

	other := newTestEnv(t, nil)
	other.loginAdmin()
	if resp, _ := other.do(other.client, "POST", "/api/admin/password", `{"current_password":"wrong","new_password":""}`); resp.StatusCode == 200 {
		t.Fatal("wrong current password deleted the password")
	}
}

func TestMoveFile(t *testing.T) {
	e := newTestEnv(t, nil)
	e.loginAdmin()

	resp, body := e.do(e.client, "POST", "/api/mkdir", `{"path":"/","name":"dest"}`)
	if resp.StatusCode != 201 {
		t.Fatalf("mkdir: %d %s", resp.StatusCode, body)
	}
	resp, body = e.do(e.client, "POST", "/api/move", `{"from":"/hello.txt","to":"/dest/hello.txt"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("move: %d %s", resp.StatusCode, body)
	}
	if resp, _ := e.anon("GET", "/api/download?path=/hello.txt", ""); resp.StatusCode != 404 {
		t.Fatal("source still exists")
	}
	if _, data := e.anon("GET", "/api/download?path=/dest/hello.txt", ""); string(data) != "hello" {
		t.Fatalf("moved content = %q", data)
	}
}

func TestMoveValidationAndRuleSync(t *testing.T) {
	e := newTestEnv(t, []Rule{{Path: "/sub/a.txt", Mode: modeLock, Password: "p"}})

	// Anonymous cannot move.
	if resp, _ := e.anon("POST", "/api/move", `{"from":"/hello.txt","to":"/sub/hello.txt"}`); resp.StatusCode != 403 {
		t.Fatalf("anon move: %d, want 403", resp.StatusCode)
	}

	e.loginAdmin()

	// Target already exists.
	if resp, _ := e.do(e.client, "POST", "/api/move", `{"from":"/hello.txt","to":"/sub/a.txt"}`); resp.StatusCode != 409 {
		t.Fatalf("move onto existing: %d, want 409", resp.StatusCode)
	}

	// Directory into itself.
	if resp, _ := e.do(e.client, "POST", "/api/move", `{"from":"/sub","to":"/sub/inside"}`); resp.StatusCode != 400 {
		t.Fatalf("move dir into itself: %d, want 400", resp.StatusCode)
	}

	// Exact-path rule follows the moved file.
	if resp, body := e.do(e.client, "POST", "/api/mkdir", `{"path":"/","name":"dest"}`); resp.StatusCode != 201 {
		t.Fatalf("mkdir: %d %s", resp.StatusCode, body)
	}
	if resp, body := e.do(e.client, "POST", "/api/move", `{"from":"/sub/a.txt","to":"/dest/a.txt"}`); resp.StatusCode != 200 {
		t.Fatalf("move locked file: %d %s", resp.StatusCode, body)
	}
	cfg, _ := e.cm.Current()
	if len(cfg.Rules) != 1 || cfg.Rules[0].Path != "/dest/a.txt" {
		t.Fatalf("rule did not follow move: %+v", cfg.Rules)
	}
}

func TestMoveRejectsRuleOverlap(t *testing.T) {
	e := newTestEnv(t, []Rule{
		{Path: "/sub", Mode: modeLock, Password: "p"},
		{Path: "/dest", Mode: modeLock, Password: "q"},
	})
	e.loginAdmin()
	if resp, body := e.do(e.client, "POST", "/api/mkdir", `{"path":"/","name":"dest"}`); resp.StatusCode != 201 {
		t.Fatalf("mkdir: %d %s", resp.StatusCode, body)
	}
	resp, body := e.do(e.client, "POST", "/api/move", `{"from":"/sub","to":"/dest/sub"}`)
	if resp.StatusCode != 409 {
		t.Fatalf("move into locked dir: %d %s", resp.StatusCode, body)
	}
	var out struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(body, &out)
	if out.Code != "rules_move_conflict" {
		t.Fatalf("code = %q, body = %s", out.Code, body)
	}
	if _, err := os.Stat(filepath.Join(e.dir, "sub", "a.txt")); err != nil {
		t.Fatalf("source should stay in place: %v", err)
	}
}

func TestZipSelected(t *testing.T) {
	e := newTestEnv(t, nil)

	// Anonymous cannot download a selection archive.
	if resp, _ := e.anon("GET", "/api/zip?path=/hello.txt", ""); resp.StatusCode != 403 {
		t.Fatalf("anon zip: %d, want 403", resp.StatusCode)
	}

	e.loginAdmin()
	if resp, body := e.do(e.client, "POST", "/api/mkdir", `{"path":"/","name":"pack"}`); resp.StatusCode != 201 {
		t.Fatalf("mkdir: %d %s", resp.StatusCode, body)
	}
	for name, content := range map[string]string{"a.txt": "aaa", "b.txt": "bbb"} {
		if resp, body := e.upload(e.client, "/pack", name, content, false); resp.StatusCode != 201 {
			t.Fatalf("upload %s: %d %s", name, resp.StatusCode, body)
		}
	}

	resp, body := e.do(e.client, "GET", "/api/zip?path=/pack&path=/hello.txt", "")
	if resp.StatusCode != 200 {
		t.Fatalf("zip: %d %s", resp.StatusCode, body)
	}
	if !strings.Contains(resp.Header.Get("Content-Type"), "application/zip") {
		t.Fatalf("content-type = %q", resp.Header.Get("Content-Type"))
	}
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, f := range zr.File {
		if strings.HasSuffix(f.Name, "/") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(rc)
		rc.Close()
		files[f.Name] = string(data)
	}
	if files["hello.txt"] != "hello" || files["pack/a.txt"] != "aaa" || files["pack/b.txt"] != "bbb" {
		t.Fatalf("zip entries = %+v", files)
	}

	// A nested selection must not appear twice.
	_, body = e.do(e.client, "GET", "/api/zip?path=/pack&path=/pack/a.txt", "")
	zr, err = zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, f := range zr.File {
		if f.Name == "pack/a.txt" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("pack/a.txt appears %d times", count)
	}
}

func TestDeleteUndo(t *testing.T) {
	e := newTestEnv(t, []Rule{{Path: "/hello.txt", Mode: modeLock, Password: "p"}})
	e.loginAdmin()

	resp, body := e.do(e.client, "POST", "/api/delete", `{"path":"/hello.txt"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("delete: %d %s", resp.StatusCode, body)
	}
	var out struct {
		Undo string `json:"undo"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.Undo == "" {
		t.Fatalf("no undo token: %s", body)
	}
	if resp, _ := e.anon("GET", "/api/download?path=/hello.txt", ""); resp.StatusCode != 404 {
		t.Fatal("file still exists after delete")
	}
	if resp, _ := e.anon("GET", "/api/download?path=/.hutch-trash", ""); resp.StatusCode != 404 {
		t.Fatal("trash is web-accessible")
	}

	resp, body = e.do(e.client, "POST", "/api/undo-delete", `{"token":"`+out.Undo+`"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("undo: %d %s", resp.StatusCode, body)
	}
	if _, data := e.do(e.client, "GET", "/api/download?path=/hello.txt", ""); string(data) != "hello" {
		t.Fatalf("restored content = %q", data)
	}
	cfg, _ := e.cm.Current()
	if len(cfg.Rules) != 1 || cfg.Rules[0].Path != "/hello.txt" {
		t.Fatalf("rules not restored: %+v", cfg.Rules)
	}

	// A token can only be used once.
	if resp, _ := e.do(e.client, "POST", "/api/undo-delete", `{"token":"`+out.Undo+`"}`); resp.StatusCode != 404 {
		t.Fatalf("second undo: %d, want 404", resp.StatusCode)
	}
}

func TestDeleteBatchAndUndoConflict(t *testing.T) {
	e := newTestEnv(t, nil)
	e.loginAdmin()

	resp, body := e.do(e.client, "POST", "/api/delete", `{"paths":["/hello.txt","/sub/a.txt"],"recursive":true}`)
	if resp.StatusCode != 200 {
		t.Fatalf("batch delete: %d %s", resp.StatusCode, body)
	}
	var out struct {
		Undo  string `json:"undo"`
		Count int    `json:"count"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.Undo == "" || out.Count != 2 {
		t.Fatalf("batch response: %s", body)
	}
	// Recreate one path, then undo must fail with 409.
	if resp, body := e.upload(e.client, "/", "hello.txt", "new", false); resp.StatusCode != 201 {
		t.Fatalf("recreate: %d %s", resp.StatusCode, body)
	}
	if resp, _ := e.do(e.client, "POST", "/api/undo-delete", `{"token":"`+out.Undo+`"}`); resp.StatusCode != 409 {
		t.Fatalf("undo conflict: %d, want 409", resp.StatusCode)
	}

	// Trash must stay hidden even with show_hidden enabled.
	if resp, body := e.do(e.client, "PUT", "/api/admin/settings", `{"site_name":"TinyHutch","session_hours":12,"max_upload_mb":10,"show_hidden":true}`); resp.StatusCode != 200 {
		t.Fatalf("settings: %d %s", resp.StatusCode, body)
	}
	resp, body = e.do(e.client, "GET", "/api/list?path=/", "")
	if strings.Contains(string(body), ".hutch-trash") {
		t.Fatalf("trash listed: %s", body)
	}
}

func TestStatSelection(t *testing.T) {
	e := newTestEnv(t, nil)
	if resp, _ := e.anon("POST", "/api/stat", `{"paths":["/hello.txt"]}`); resp.StatusCode != 403 {
		t.Fatalf("anon stat: %d, want 403", resp.StatusCode)
	}
	e.loginAdmin()
	if resp, body := e.do(e.client, "POST", "/api/mkdir", `{"path":"/","name":"pack"}`); resp.StatusCode != 201 {
		t.Fatalf("mkdir: %d %s", resp.StatusCode, body)
	}
	for name, content := range map[string]string{"a.txt": "aaa", "b.txt": "bbb"} {
		if resp, body := e.upload(e.client, "/pack", name, content, false); resp.StatusCode != 201 {
			t.Fatalf("upload: %d %s", resp.StatusCode, body)
		}
	}
	resp, body := e.do(e.client, "POST", "/api/stat", `{"paths":["/pack","/hello.txt"]}`)
	if resp.StatusCode != 200 {
		t.Fatalf("stat: %d %s", resp.StatusCode, body)
	}
	var out struct {
		Files int   `json:"files"`
		Bytes int64 `json:"bytes"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	if out.Files != 3 || out.Bytes != 11 {
		t.Fatalf("stat = %+v, want 3 files / 11 bytes", out)
	}
	// Nested selections are counted once.
	_, body = e.do(e.client, "POST", "/api/stat", `{"paths":["/pack","/pack/a.txt"]}`)
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	if out.Files != 2 || out.Bytes != 6 {
		t.Fatalf("nested stat = %+v", out)
	}
}

func TestDownloadWithPassword(t *testing.T) {
	e := newTestEnv(t, []Rule{{Path: "/private", Mode: modeLock, Password: "pw"}})
	writeTestFile(t, filepath.Join(e.dir, "private", "x.txt"), "secret")

	for _, u := range []string{
		"/api/download?path=/private/x.txt",
		"/api/download?path=/private/x.txt&password=wrong",
	} {
		if resp, _ := e.anon("GET", u, ""); resp.StatusCode != 401 {
			t.Fatalf("%s: %d, want 401", u, resp.StatusCode)
		}
	}
	resp, body := e.anon("GET", "/api/download?path=/private/x.txt&password=pw", "")
	if resp.StatusCode != 200 || string(body) != "secret" {
		t.Fatalf("password download: %d %q", resp.StatusCode, body)
	}
	// A password on an unlocked file is simply ignored.
	resp, body = e.anon("GET", "/api/download?path=/hello.txt&password=anything", "")
	if resp.StatusCode != 200 || string(body) != "hello" {
		t.Fatalf("unlocked download: %d %q", resp.StatusCode, body)
	}
}

func TestDownloadPasswordRateLimit(t *testing.T) {
	e := newTestEnv(t, []Rule{{Path: "/private", Mode: modeLock, Password: "pw"}})
	writeTestFile(t, filepath.Join(e.dir, "private", "x.txt"), "secret")

	for i := 0; i < limiterMaxFail-1; i++ {
		resp, _ := e.anon("GET", "/api/download?path=/private/x.txt&password=bad", "")
		if resp.StatusCode != 401 {
			t.Fatalf("attempt %d: %d, want 401", i+1, resp.StatusCode)
		}
	}
	if resp, _ := e.anon("GET", "/api/download?path=/private/x.txt&password=bad", ""); resp.StatusCode != 429 {
		t.Fatalf("fifth attempt: %d, want 429", resp.StatusCode)
	}
	if resp, _ := e.anon("GET", "/api/download?path=/private/x.txt&password=pw", ""); resp.StatusCode != 429 {
		t.Fatalf("correct password during ban: %d, want 429", resp.StatusCode)
	}
}

func TestRulePasswordForLink(t *testing.T) {
	e := newTestEnv(t, []Rule{{Path: "/private", Mode: modeLock, Password: "parent"}})
	writeTestFile(t, filepath.Join(e.dir, "private", "x.txt"), "x")
	writeTestFile(t, filepath.Join(e.dir, "private", "secret.txt"), "s")

	type pwResp struct {
		Password string `json:"password"`
	}
	get := func(c *http.Client, p string) (int, string) {
		resp, body := e.do(c, "GET", "/api/rule-password?path="+url.QueryEscape(p), "")
		var out pwResp
		_ = json.Unmarshal(body, &out)
		return resp.StatusCode, out.Password
	}

	if code, _ := get(newClient(), "/private/x.txt"); code != 401 {
		t.Fatalf("anonymous locked: %d, want 401", code)
	}
	if code, pw := get(newClient(), "/hello.txt"); code != 200 || pw != "" {
		t.Fatalf("unprotected: %d %q", code, pw)
	}

	e.loginAdmin()
	if code, pw := get(e.client, "/private/x.txt"); code != 200 || pw != "parent" {
		t.Fatalf("admin: %d %q", code, pw)
	}

	reader := newClient()
	if resp, body := e.unlock(reader, "/private", "parent"); resp.StatusCode != 200 {
		t.Fatalf("unlock parent: %d %s", resp.StatusCode, body)
	}
	if code, pw := get(reader, "/private/x.txt"); code != 200 || pw != "parent" {
		t.Fatalf("unlocked parent: %d %q", code, pw)
	}
	if code, pw := get(reader, "/private/secret.txt"); code != 200 || pw != "parent" {
		t.Fatalf("inherited password: %d %q", code, pw)
	}
	if code, _ := get(reader, "/nope.txt"); code != 404 {
		t.Fatalf("missing: %d, want 404", code)
	}
	if code, _ := get(reader, "/private"); code != 400 {
		t.Fatalf("directory: %d, want 400", code)
	}
}

func TestRulePasswordChangeRelocks(t *testing.T) {
	e := newTestEnv(t, []Rule{{Path: "/sub", Mode: modeLock, Password: "p1"}})
	c := newClient()
	if resp, body := e.unlock(c, "/sub", "p1"); resp.StatusCode != 200 {
		t.Fatalf("unlock: %d %s", resp.StatusCode, body)
	}
	if resp, _ := e.do(c, "GET", "/api/list?path=/sub", ""); resp.StatusCode != 200 {
		t.Fatal("should be unlocked")
	}
	e.setRules(Rule{Path: "/sub", Mode: modeLock, Password: "p2"})
	if resp, _ := e.do(c, "GET", "/api/list?path=/sub", ""); resp.StatusCode != 401 {
		t.Fatal("old unlock must not survive a password change")
	}
}

func TestProtectedAndHiddenFiles(t *testing.T) {
	e := newTestEnv(t, nil)
	for _, p := range []string{"/config.json", "/config.json.bak", "/.hutch.secret"} {
		if resp, _ := e.anon("GET", "/api/download?path="+p, ""); resp.StatusCode != 404 {
			t.Errorf("%s: %d, want 404", p, resp.StatusCode)
		}
	}
	writeTestFile(t, filepath.Join(e.dir, ".env"), "SECRET=1")
	if resp, _ := e.anon("GET", "/api/download?path=/.env", ""); resp.StatusCode != 404 {
		t.Fatal("hidden file should be 404")
	}

	e.loginAdmin()
	resp, body := e.do(e.client, "PUT", "/api/admin/settings",
		`{"site_name":"TinyHutch","session_hours":12,"max_upload_mb":10,"show_hidden":true}`)
	if resp.StatusCode != 200 {
		t.Fatalf("settings: %d %s", resp.StatusCode, body)
	}
	resp, body = e.do(e.client, "GET", "/api/list?path=/", "")
	if !strings.Contains(string(body), ".env") {
		t.Fatalf("hidden file should be listed when enabled: %s", body)
	}
	if strings.Contains(string(body), `"config.json"`) || strings.Contains(string(body), ".hutch.secret") {
		t.Fatalf("protected files must never be listed: %s", body)
	}
}

func TestTraversalAndSymlink(t *testing.T) {
	e := newTestEnv(t, nil)
	for _, p := range []string{
		"/api/download?path=/../etc/passwd",
		"/api/download?path=/%2e%2e/%2e%2e/etc/passwd",
		"/api/download?path=/..%2f..%2fetc%2fpasswd",
	} {
		resp, _ := e.anon("GET", p, "")
		if resp.StatusCode != 400 && resp.StatusCode != 404 {
			t.Errorf("%s: %d", p, resp.StatusCode)
		}
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(e.dir, "escape.txt")); err != nil {
		t.Fatal(err)
	}
	if resp, _ := e.anon("GET", "/api/download?path=/escape.txt", ""); resp.StatusCode != 404 {
		t.Fatal("symlink escape must be blocked")
	}
}

func TestUploadOverwrite(t *testing.T) {
	e := newTestEnv(t, nil)
	e.loginAdmin()
	resp, _ := e.upload(e.client, "/", "hello.txt", "new", false)
	if resp.StatusCode != 409 {
		t.Fatalf("overwrite=0: %d, want 409", resp.StatusCode)
	}
	resp, body := e.upload(e.client, "/", "hello.txt", "new", true)
	if resp.StatusCode != 201 {
		t.Fatalf("overwrite=1: %d %s", resp.StatusCode, body)
	}
	_, data := e.anon("GET", "/api/download?path=/hello.txt", "")
	if string(data) != "new" {
		t.Fatalf("content = %q", data)
	}
}

func TestUploadLimit(t *testing.T) {
	e := newTestEnv(t, nil)
	e.loginAdmin()
	resp, body := e.do(e.client, "PUT", "/api/admin/settings",
		`{"site_name":"TinyHutch","session_hours":12,"max_upload_mb":1,"show_hidden":false}`)
	if resp.StatusCode != 200 {
		t.Fatalf("settings: %d %s", resp.StatusCode, body)
	}
	resp, _ = e.upload(e.client, "/", "big.bin", strings.Repeat("x", 2<<20), false)
	if resp.StatusCode != 413 {
		t.Fatalf("oversized upload: %d, want 413", resp.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(e.dir, "big.bin")); !os.IsNotExist(err) {
		t.Fatal("oversized upload left a file behind")
	}
}

func TestRateLimit(t *testing.T) {
	e := newTestEnv(t, nil)
	c := newClient()
	for i := 0; i < limiterMaxFail-1; i++ {
		resp, _ := e.unlock(c, "/", "wrong")
		if resp.StatusCode != 401 {
			t.Fatalf("attempt %d: %d, want 401", i+1, resp.StatusCode)
		}
	}
	resp, _ := e.unlock(c, "/", "wrong")
	if resp.StatusCode != 429 {
		t.Fatalf("fifth attempt: %d, want 429", resp.StatusCode)
	}
	resp, _ = e.unlock(c, "/", e.adminPW)
	if resp.StatusCode != 429 {
		t.Fatalf("correct password during ban: %d, want 429", resp.StatusCode)
	}
}

func TestRuleTestAPI(t *testing.T) {
	e := newTestEnv(t, []Rule{{Path: "/sub", Mode: modeLock, Password: "p"}})
	e.loginAdmin()
	resp, body := e.do(e.client, "POST", "/api/admin/rule-test", `{"path":"/sub/a.txt"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("rule test: %d %s", resp.StatusCode, body)
	}
	var out struct {
		Locked       bool  `json:"locked"`
		Readable     bool  `json:"readable"`
		RuleIndex    int   `json:"rule_index"`
		MatchedRules []int `json:"matched_rules"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	if !out.Locked || !out.Readable || out.RuleIndex != 0 || len(out.MatchedRules) != 1 {
		t.Fatalf("unexpected rule test result: %+v", out)
	}
}

func TestRenameAndDeleteSyncExactRules(t *testing.T) {
	e := newTestEnv(t, []Rule{{Path: "/sub/a.txt", Mode: modeLock, Password: "p"}})
	e.loginAdmin()

	resp, body := e.do(e.client, "POST", "/api/rename", `{"path":"/sub/a.txt","name":"b.txt"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("rename: %d %s", resp.StatusCode, body)
	}
	cfg, _ := e.cm.Current()
	if len(cfg.Rules) != 1 || cfg.Rules[0].Path != "/sub/b.txt" {
		t.Fatalf("rule not followed rename: %+v", cfg.Rules)
	}

	resp, body = e.do(e.client, "POST", "/api/delete", `{"path":"/sub/b.txt"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("delete: %d %s", resp.StatusCode, body)
	}
	cfg, _ = e.cm.Current()
	if len(cfg.Rules) != 0 {
		t.Fatalf("rule not cleaned after delete: %+v", cfg.Rules)
	}
}

func TestCSRFHeaderRequired(t *testing.T) {
	e := newTestEnv(t, nil)
	req, _ := http.NewRequest("POST", e.ts.URL+"/api/mkdir", strings.NewReader(`{"path":"/","name":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatalf("missing X-Requested-With: %d, want 403", resp.StatusCode)
	}
}
