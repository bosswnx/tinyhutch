package main

import (
	"errors"
	"testing"
)

func TestCleanRelPath(t *testing.T) {
	ok := map[string]string{
		"":        "",
		"/":       "",
		"/a/b":    "a/b",
		"a/b":     "a/b",
		"/a//b":   "a/b",
		"/a/./b":  "a/b",
		"/a/b/..": "a",
		"/a%2fb":  "a%2fb",
	}
	for in, want := range ok {
		got, err := cleanRelPath(in)
		if err != nil {
			t.Fatalf("cleanRelPath(%q): %v", in, err)
		}
		if got != want {
			t.Errorf("cleanRelPath(%q) = %q, want %q", in, got, want)
		}
	}
	for _, in := range []string{"/../etc/passwd", "a/../../b", "../../x"} {
		if _, err := cleanRelPath(in); err == nil {
			t.Errorf("cleanRelPath(%q) should fail", in)
		}
	}
}

func TestNormalizeAbsPath(t *testing.T) {
	cases := map[string]string{
		"":        "/",
		"/":       "/",
		"docs":    "/docs",
		"/docs/":  "/docs",
		"/a/../b": "/b",
	}
	for in, want := range cases {
		got, err := normalizeAbsPath(in)
		if err != nil {
			t.Fatalf("normalizeAbsPath(%q): %v", in, err)
		}
		if got != want {
			t.Errorf("normalizeAbsPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEvaluateInheritsDirectoryLock(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	rules, err := compileRules([]Rule{
		{Path: "/p", Mode: modeLock, Password: "one"},
		{Path: "/x", Password: "two"},
	}, secret)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		path string
		pass string
		rule string
	}{
		{"/p", "one", "/p"},
		{"/p/a", "one", "/p"},
		{"/p/a/b", "one", "/p"},
		{"/pfoo", "", ""},
		{"/x/a", "two", "/x"},
		{"/other", "", ""},
		{"/", "", ""},
	}
	for _, c := range cases {
		e := evaluate(rules, c.path)
		if e.Password != c.pass || e.RulePath != c.rule {
			t.Errorf("evaluate(%q) = password %q rule %q, want %q / %q",
				c.path, e.Password, e.RulePath, c.pass, c.rule)
		}
		if c.pass == "" && e.RuleIndex != -1 {
			t.Errorf("evaluate(%q) RuleIndex = %d, want -1", c.path, e.RuleIndex)
		}
	}

	// Unlocking a directory covers its whole subtree.
	if a, b := evaluate(rules, "/p/a"), evaluate(rules, "/p/b"); a.RuleID != b.RuleID {
		t.Fatal("same directory rule must produce the same ID for its subtree")
	}
	if a, b := evaluate(rules, "/p/a"), evaluate(rules, "/x/a"); a.RuleID == b.RuleID {
		t.Fatal("different rules must have different IDs")
	}
}

func TestRuleIDsArePathScoped(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	if ruleID(secret, "/a", "same") == ruleID(secret, "/b", "same") {
		t.Fatal("identical passwords on different paths must produce different IDs")
	}
	if ruleID(secret, "/a", "same") == ruleID([]byte("another-secret-0000000000000000"), "/a", "same") {
		t.Fatal("IDs must depend on the secret")
	}
}

func TestCompileRulesValidation(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	bad := [][]Rule{
		{{Path: "/a", Mode: modeLock}},
		{{Path: "/a", Mode: modeLegacyPublic}},
		{{Path: "/a", Mode: "nope", Password: "p"}},
		{{Path: "/a/*.txt", Mode: modeLock, Password: "p"}},
		{{Path: "/a/../b", Mode: modeLock, Password: "p"}},
	}
	for _, rs := range bad {
		if _, err := compileRules(rs, secret); err == nil {
			t.Errorf("compileRules(%+v) should fail", rs)
		}
	}
}

func TestCompileRulesRejectsOverlap(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	cases := [][]Rule{
		{
			{Path: "/a", Mode: modeLock, Password: "p"},
			{Path: "/a", Mode: modeLock, Password: "q"},
		},
		{
			{Path: "/a", Mode: modeLock, Password: "p"},
			{Path: "/a/b", Mode: modeLock, Password: "q"},
		},
		{
			{Path: "/a/b", Mode: modeLock, Password: "p"},
			{Path: "/a", Mode: modeLock, Password: "q"},
		},
		{
			{Path: "/", Mode: modeLock, Password: "p"},
			{Path: "/a", Mode: modeLock, Password: "q"},
		},
	}
	for _, rs := range cases {
		_, err := compileRules(rs, secret)
		if !errors.Is(err, errRuleOverlap) {
			t.Errorf("compileRules(%+v) error = %v, want rule overlap", rs, err)
		}
	}
}
