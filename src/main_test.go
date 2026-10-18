package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseArgs(t *testing.T) {
	base := t.TempDir()
	sub := filepath.Join(base, "files")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "note.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		args []string
		root string
		err  bool
	}{
		{"default", nil, base, false},
		{"dot", []string{"."}, base, false},
		{"relative", []string{"files"}, sub, false},
		{"absolute", []string{sub}, sub, false},
		{"file", []string{"note.txt"}, "", true},
		{"missing", []string{"nope"}, "", true},
		{"too many", []string{"a", "b"}, "", true},
		{"no subcommands", []string{"passwd"}, "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root, err := parseArgs(c.args, base)
			if (err != nil) != c.err {
				t.Fatalf("err = %v, wantErr %v", err, c.err)
			}
			if c.err {
				return
			}
			if root != c.root {
				t.Fatalf("root = %q, want %q", root, c.root)
			}
		})
	}
}

func TestReorderArgs(t *testing.T) {
	cases := []struct{ in, want []string }{
		{[]string{"--config", "c.json", "/data"}, []string{"--config", "c.json", "/data"}},
		{[]string{"/data", "--config", "c.json"}, []string{"--config", "c.json", "/data"}},
		{[]string{"--version", "/data"}, []string{"--version", "/data"}},
		{[]string{"--", "-weird"}, []string{"--", "-weird"}},
		{[]string{"--config=c.json", "/data"}, []string{"--config=c.json", "/data"}},
	}
	for _, c := range cases {
		if got := reorderArgs(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("reorderArgs(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestResolveConfigPath(t *testing.T) {
	root := t.TempDir()
	cwd := t.TempDir()

	if got := resolveConfigPath(root, "", cwd); got != filepath.Join(root, "config.json") {
		t.Fatalf("default = %q", got)
	}
	if got := resolveConfigPath(root, "custom.json", cwd); got != filepath.Join(cwd, "custom.json") {
		t.Fatalf("relative override = %q", got)
	}
	abs := filepath.Join(t.TempDir(), "abs.json")
	if got := resolveConfigPath(root, abs, cwd); got != abs {
		t.Fatalf("absolute override = %q", got)
	}
}
