package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
)

// Rule is one entry of the ordered rule list stored in the config file.
type Rule struct {
	Path     string `json:"path"`
	Mode     string `json:"mode"`
	Password string `json:"password,omitempty"`
}

const (
	modeLock   = "lock"
	modePublic = "public"
)

type compiledRule struct {
	pattern string
	mode    string
	pass    string
	id      string // rule ID used by sessions; only meaningful for lock rules
	re      *regexp.Regexp
}

// Effective is the result of evaluating the ordered rules for one path.
type Effective struct {
	Password  string // empty = public
	RuleID    string // empty = no lock rule in effect
	RuleIndex int    // -1 = no rule matched
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

var errBadGlob = errors.New("invalid glob pattern")

// compileGlob converts the tiny glob language into a regexp.
//
//   - -> [^/]*
//     **  -> .*   (when followed by "/", matches zero or more whole segments)
//     ?   -> [^/]
func compileGlob(pattern string) (*regexp.Regexp, error) {
	if !strings.HasPrefix(pattern, "/") {
		return nil, errBadGlob
	}
	suffix := ""
	p := pattern
	if strings.HasSuffix(p, "/**") {
		p = strings.TrimSuffix(p, "/**")
		suffix = "(?:/.*)?"
	}
	var b strings.Builder
	b.WriteByte('^')
	for i := 0; i < len(p); {
		switch p[i] {
		case '*':
			if i+1 < len(p) && p[i+1] == '*' {
				i += 2
				if i < len(p) && p[i] == '/' {
					b.WriteString("(?:.*/)?")
					i++
				} else {
					b.WriteString(".*")
				}
			} else {
				b.WriteString("[^/]*")
				i++
			}
		case '?':
			b.WriteString("[^/]")
			i++
		default:
			b.WriteString(regexp.QuoteMeta(p[i : i+1]))
			i++
		}
	}
	b.WriteString(suffix)
	b.WriteByte('$')
	return regexp.Compile(b.String())
}

func ruleID(secret []byte, rulePath, password string) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("rule\x00"))
	mac.Write([]byte(rulePath))
	mac.Write([]byte{0})
	mac.Write([]byte(password))
	return hex.EncodeToString(mac.Sum(nil))[:16]
}

func isExactPath(p string) bool { return !strings.ContainsAny(p, "*?") }

func compileRules(rules []Rule, secret []byte) ([]compiledRule, error) {
	out := make([]compiledRule, 0, len(rules))
	for i, r := range rules {
		abs, err := normalizeAbsPath(r.Path)
		if err != nil {
			return nil, fmt.Errorf("规则 #%d: %w", i+1, err)
		}
		if abs != r.Path {
			return nil, fmt.Errorf("规则 #%d: 路径未规范化，应为 %q", i+1, abs)
		}
		switch r.Mode {
		case modeLock:
			if r.Password == "" {
				return nil, fmt.Errorf("规则 #%d (%s): lock 必须设置密码", i+1, r.Path)
			}
		case modePublic:
			if r.Password != "" {
				return nil, fmt.Errorf("规则 #%d (%s): public 不能设置密码", i+1, r.Path)
			}
		default:
			return nil, fmt.Errorf("规则 #%d: 未知模式 %q", i+1, r.Mode)
		}
		re, err := compileGlob(abs)
		if err != nil {
			return nil, fmt.Errorf("规则 #%d (%s): %w", i+1, r.Path, err)
		}
		cr := compiledRule{pattern: abs, mode: r.Mode, pass: r.Password, re: re}
		if r.Mode == modeLock {
			cr.id = ruleID(secret, abs, r.Password)
		}
		out = append(out, cr)
	}
	return out, nil
}

// evaluate walks the rules in order; later rules override earlier ones.
func evaluate(rules []compiledRule, abs string) Effective {
	e := Effective{RuleIndex: -1}
	for i := range rules {
		if !rules[i].re.MatchString(abs) {
			continue
		}
		switch rules[i].mode {
		case modeLock:
			e.Password = rules[i].pass
			e.RuleID = rules[i].id
			e.RuleIndex = i
		case modePublic:
			e = Effective{RuleIndex: -1}
		}
	}
	return e
}

// matchedRuleIndexes is used by the rule tester.
func matchedRuleIndexes(rules []compiledRule, abs string) []int {
	var out []int
	for i := range rules {
		if rules[i].re.MatchString(abs) {
			out = append(out, i)
		}
	}
	return out
}
