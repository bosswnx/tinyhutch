package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigCreateAndReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	cfg, created, err := loadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("expected a newly created config")
	}
	if cfg.AdminPassword != "" {
		t.Fatalf("admin_password should default to empty, got %q", cfg.AdminPassword)
	}
	if cfg.SiteName != "TinyHutch" || cfg.SessionHours != defaultSessionHours || cfg.MaxUploadMB != defaultMaxUploadMB {
		t.Fatalf("defaults not applied: %+v", cfg)
	}
	if cfg.ListenHost != "127.0.0.1" || cfg.ListenPort != 8080 || cfg.LogLevel != "info" || cfg.InsecureCookie || cfg.TrustProxy {
		t.Fatalf("network/log defaults not applied: %+v", cfg)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o", st.Mode().Perm())
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"site_name": "TinyHutch"`) {
		t.Fatalf("config is not JSON:\n%s", raw)
	}

	cfg2, created2, err := loadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if created2 || cfg2.AdminPassword != "" {
		t.Fatal("reload mismatch")
	}
}

func TestConfigUnknownField(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"admin_password":"x","bogus":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadConfig(path); err == nil {
		t.Fatal("unknown fields must be rejected")
	}
}

func TestConfigMissingFields(t *testing.T) {
	full := map[string]any{
		"site_name":       "TinyHutch",
		"listen_host":     "127.0.0.1",
		"listen_port":     8080,
		"admin_password":  "pw",
		"session_hours":   12,
		"max_upload_mb":   4096,
		"show_hidden":     false,
		"log_level":       "info",
		"trust_proxy":     false,
		"insecure_cookie": false,
		"rules":           []any{},
	}
	keys := []string{
		"site_name", "listen_host", "listen_port", "admin_password", "session_hours",
		"max_upload_mb", "show_hidden", "log_level", "trust_proxy", "insecure_cookie", "rules",
	}
	dir := t.TempDir()
	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			m := make(map[string]any, len(full)-1)
			for k, v := range full {
				if k != key {
					m[k] = v
				}
			}
			data, err := json.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, key+".json")
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			_, _, err = loadConfig(path)
			if err == nil {
				t.Fatalf("missing %q should fail", key)
			}
			if !strings.Contains(err.Error(), "配置缺少字段") || !strings.Contains(err.Error(), key) {
				t.Fatalf("error = %v, want mention of %q", err, key)
			}
		})
	}
}

func TestConfigValidation(t *testing.T) {
	bad := []*Config{
		{Rules: []Rule{{Path: "", Mode: modeLock, Password: "p"}}},
		{Rules: []Rule{{Path: "/a", Mode: modeLock}}},
		{Rules: []Rule{{Path: "/a", Mode: "write", Password: "p"}}},
		{Rules: []Rule{{Path: "/a/*.txt", Mode: modeLock, Password: "p"}}},
		{Rules: []Rule{
			{Path: "/a", Mode: modeLock, Password: "p"},
			{Path: "/a/b", Mode: modeLock, Password: "q"},
		}},
		{SessionHours: 100000},
		{MaxUploadMB: -1},
		{ListenPort: -1},
		{ListenPort: 70000},
		{ListenHost: "127.0.0.1:9000"},
		{ListenHost: "bad host"},
		{LogLevel: "verbose"},
	}
	for i, c := range bad {
		if err := validateConfig(c); err == nil {
			t.Errorf("case %d should fail: %+v", i, c)
		}
	}
	good := &Config{Rules: []Rule{
		{Path: "docs/**", Mode: modeLock, Password: "p"},
		{Path: "/private", Password: "q"},
	}}
	if err := validateConfig(good); err != nil {
		t.Fatal(err)
	}
	if good.Rules[0].Path != "/docs" {
		t.Fatalf("legacy /** path not normalized: %q", good.Rules[0].Path)
	}
	if good.Rules[1].Mode != modeLock {
		t.Fatalf("empty mode not normalized: %q", good.Rules[1].Mode)
	}
	public := &Config{Rules: []Rule{
		{Path: "/a", Mode: modeLock, Password: "p"},
		{Path: "/a/open", Mode: modeLegacyPublic},
	}}
	if err := validateConfig(public); err != nil {
		t.Fatal(err)
	}
	if len(public.Rules) != 1 || public.Rules[0].Path != "/a" {
		t.Fatalf("public rule was not removed: %+v", public.Rules)
	}
	if err := validateConfig(&Config{ListenHost: "::1", ListenPort: 8080}); err != nil {
		t.Fatalf("ipv6 host rejected: %v", err)
	}
}

func TestListenAddr(t *testing.T) {
	cases := []struct {
		host string
		port int
		want string
	}{
		{"127.0.0.1", 8080, "127.0.0.1:8080"},
		{"0.0.0.0", 80, "0.0.0.0:80"},
		{"::1", 8080, "[::1]:8080"},
		{"localhost", 9000, "localhost:9000"},
	}
	for _, c := range cases {
		if got := (&Config{ListenHost: c.host, ListenPort: c.port}).ListenAddr(); got != c.want {
			t.Errorf("ListenAddr(%q, %d) = %q, want %q", c.host, c.port, got, c.want)
		}
	}
}

func TestSaveConfigKeepsBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	cfg := &Config{AdminPassword: "one"}
	if err := saveConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	cfg.AdminPassword = "two"
	if err := saveConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path + ".bak")
	if err != nil {
		t.Fatal("backup missing:", err)
	}
	if string(data) == "" || string(data) == string(mustRead(t, path)) {
		t.Fatal("backup does not contain the previous config")
	}
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestEnsureSecret(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".tinyhutch.secret")
	s1, err := ensureSecret(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(s1) != 32 {
		t.Fatalf("len = %d", len(s1))
	}
	s2, err := ensureSecret(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(s1) != string(s2) {
		t.Fatal("secret not stable")
	}
	if err := os.WriteFile(path, []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ensureSecret(path); err == nil {
		t.Fatal("invalid secret length must fail")
	}
}
