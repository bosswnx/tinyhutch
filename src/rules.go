package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"strings"
)

// Rule is one entry of the rule list stored in the config file.
//
// A rule locks a single path. Locking a directory also locks everything below
// it, so rule paths must not overlap (one path cannot be the same as, or
// contain, another rule path).
type Rule struct {
	Path     string `json:"path"`
	Mode     string `json:"mode,omitempty"`
	Password string `json:"password,omitempty"`
}

const (
	modeLock = "lock"
	// modeLegacyPublic is the removed "public" mode. It is dropped while
	// loading old configs because locks can no longer be cancelled per path.
	modeLegacyPublic = "public"
)

var errRuleOverlap = errors.New("rule paths overlap")

type compiledRule struct {
	pattern string
	pass    string
	id      string // rule ID used by sessions
}

// Effective is the result of evaluating the rules for one path.
type Effective struct {
	Password  string // empty = public
	RuleID    string // empty = no lock rule in effect
	RuleIndex int    // -1 = no rule matched
	RulePath  string // path of the rule in effect; empty = public
}

func (e Effective) Locked() bool { return e.Password != "" }

// normalizeAbsPath turns user input into a clean absolute slash path.
func normalizeAbsPath(p string) (string, error) {
	if strings.IndexByte(p, 0) >= 0 {
		return "", errors.New("path contains NUL")
	}
	if p == "" {
		return "/", nil
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	clean := path.Clean(p)
	if clean == "." {
		return "/", nil
	}
	return clean, nil
}

// cleanRelPath turns a request path into a relative path without leading slash.
// Returns "" for the root directory. Escapes above the root are rejected.
func cleanRelPath(p string) (string, error) {
	if strings.IndexByte(p, 0) >= 0 {
		return "", errors.New("path contains NUL")
	}
	p = strings.TrimPrefix(p, "/")
	if p == "" {
		return "", nil
	}
	clean := path.Clean(p)
	if clean == "." {
		return "", nil
	}
	if clean == ".." || strings.HasPrefix(clean, "../") || path.IsAbs(clean) {
		return "", errors.New("path escapes root")
	}
	return clean, nil
}

func absPath(rel string) string {
	if rel == "" {
		return "/"
	}
	return "/" + rel
}

func joinRel(dir, name string) string {
	if dir == "" {
		return name
	}
	return dir + "/" + name
}

func parentOf(rel string) string {
	if rel == "" {
		return ""
	}
	p := path.Dir(rel)
	if p == "." {
		return ""
	}
	return p
}

// pathContains reports whether child is parent itself or lives below parent.
// Paths are clean absolute slash paths.
func pathContains(parent, child string) bool {
	if parent == child {
		return true
	}
	if parent == "/" {
		return strings.HasPrefix(child, "/")
	}
	return strings.HasPrefix(child, parent+"/")
}

func pathsOverlap(a, b string) bool {
	return pathContains(a, b) || pathContains(b, a)
}

func isExactPath(p string) bool { return !strings.ContainsAny(p, "*?") }

func ruleID(secret []byte, rulePath, password string) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("rule\x00"))
	mac.Write([]byte(rulePath))
	mac.Write([]byte{0})
	mac.Write([]byte(password))
	return hex.EncodeToString(mac.Sum(nil))[:16]
}

func compileRules(rules []Rule, secret []byte) ([]compiledRule, error) {
	out := make([]compiledRule, 0, len(rules))
	for i, r := range rules {
		abs, err := normalizeAbsPath(r.Path)
		if err != nil {
			return nil, fmt.Errorf("rule #%d: %w", i+1, err)
		}
		if abs != r.Path {
			return nil, fmt.Errorf("rule #%d: path is not normalized; expected %q", i+1, abs)
		}
		if r.Mode != "" && r.Mode != modeLock {
			return nil, fmt.Errorf("rule #%d: only lock mode is supported", i+1)
		}
		if r.Password == "" {
			return nil, fmt.Errorf("rule #%d (%s): a password is required", i+1, r.Path)
		}
		if !isExactPath(abs) {
			return nil, fmt.Errorf("rule #%d (%s): path cannot contain wildcards", i+1, r.Path)
		}
		for j := range out {
			if pathsOverlap(out[j].pattern, abs) {
				return nil, fmt.Errorf("%w: rule #%d (%s) overlaps rule #%d (%s); locked paths cannot contain each other",
					errRuleOverlap, i+1, abs, j+1, out[j].pattern)
			}
		}
		out = append(out, compiledRule{
			pattern: abs,
			pass:    r.Password,
			id:      ruleID(secret, abs, r.Password),
		})
	}
	return out, nil
}

// evaluate finds the lock rule that covers abs. Overlapping rules are rejected
// at compile time, so at most one rule matches; the longest path wins if a
// caller ever passes an unvalidated list.
func evaluate(rules []compiledRule, abs string) Effective {
	best := -1
	for i := range rules {
		if !pathContains(rules[i].pattern, abs) {
			continue
		}
		if best < 0 || len(rules[i].pattern) > len(rules[best].pattern) {
			best = i
		}
	}
	if best < 0 {
		return Effective{RuleIndex: -1}
	}
	return Effective{
		Password:  rules[best].pass,
		RuleID:    rules[best].id,
		RuleIndex: best,
		RulePath:  rules[best].pattern,
	}
}

// matchedRuleIndexes is used by the rule tester.
func matchedRuleIndexes(rules []compiledRule, abs string) []int {
	var out []int
	for i := range rules {
		if pathContains(rules[i].pattern, abs) {
			out = append(out, i)
		}
	}
	return out
}
