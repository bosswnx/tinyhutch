package main

import "testing"

func TestCompileGlob(t *testing.T) {
	cases := []struct {
		pattern string
		path    string
		want    bool
	}{
		{"/private/**", "/private", true},
		{"/private/**", "/private/a.txt", true},
		{"/private/**", "/private/a/b", true},
		{"/private/**", "/privatex", false},
		{"/private/**", "/", false},
		{"/**/*.zip", "/data.zip", true},
		{"/**/*.zip", "/a/b.zip", true},
		{"/**/*.zip", "/a/b.txt", false},
		{"/a/?/c", "/a/b/c", true},
		{"/a/?/c", "/a/bb/c", false},
		{"/a/*.txt", "/a/b.txt", true},
		{"/a/*.txt", "/a/b/c.txt", false},
		{"/", "/", true},
		{"/**", "/a/b", true},
		{"/**", "/", true},
		{"/a/[b]", "/a/[b]", true},
	}
	for _, c := range cases {
		re, err := compileGlob(c.pattern)
		if err != nil {
			t.Fatalf("compile %q: %v", c.pattern, err)
		}
		if got := re.MatchString(c.path); got != c.want {
			t.Errorf("%q.Match(%q) = %v, want %v", c.pattern, c.path, got, c.want)
		}
	}
}

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

func TestEvaluateOrder(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	rules, err := compileRules([]Rule{
		{Path: "/p/**", Mode: modeLock, Password: "one"},
		{Path: "/p/x/**", Mode: modeLock, Password: "two"},
		{Path: "/p/x/open/**", Mode: modePublic},
	}, secret)
	if err != nil {
		t.Fatal(err)
	}

	if e := evaluate(rules, "/p/a"); e.Password != "one" {
		t.Fatalf("/p/a password = %q", e.Password)
	}
	if e := evaluate(rules, "/p/x/a"); e.Password != "two" {
		t.Fatalf("/p/x/a password = %q", e.Password)
	}
	if e := evaluate(rules, "/p/x/open/a"); e.Password != "" {
		t.Fatalf("/p/x/open/a should be public, got %q", e.Password)
	}
	if e := evaluate(rules, "/other"); e.Password != "" {
		t.Fatalf("/other should be public")
	}
	// Same rule -> same ID, so unlocking the directory covers its subtree.
	if a, b := evaluate(rules, "/p/a"), evaluate(rules, "/p/x"); a.RuleID == b.RuleID {
		t.Fatalf("different rules must have different IDs")
	}
	if a, b := evaluate(rules, "/p/a"), evaluate(rules, "/p/b"); a.RuleID != b.RuleID {
		t.Fatalf("same rule must have the same ID")
	}
}

func TestRuleIDsArePathScoped(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	if ruleID(secret, "/a/**", "same") == ruleID(secret, "/b/**", "same") {
		t.Fatal("identical passwords on different paths must produce different IDs")
	}
	if ruleID(secret, "/a/**", "same") == ruleID([]byte("another-secret-0000000000000000"), "/a/**", "same") {
		t.Fatal("IDs must depend on the secret")
	}
}

func TestCompileRulesValidation(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	bad := [][]Rule{
		{{Path: "/a", Mode: modeLock}},
		{{Path: "/a", Mode: modePublic, Password: "x"}},
		{{Path: "/a", Mode: "nope"}},
	}
	for _, rs := range bad {
		if _, err := compileRules(rs, secret); err == nil {
			t.Errorf("compileRules(%+v) should fail", rs)
		}
	}
}
