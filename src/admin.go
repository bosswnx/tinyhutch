package main

import (
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"syscall"
	"time"
)

type adminConfigResponse struct {
	SiteName         string `json:"site_name"`
	Rules            []Rule `json:"rules"`
	SessionHours     int    `json:"session_hours"`
	MaxUploadMB      int64  `json:"max_upload_mb"`
	ShowHidden       bool   `json:"show_hidden"`
	AdminPasswordSet bool   `json:"admin_password_set"`
	Version          string `json:"version"`
	Root             string `json:"root"`
	ConfigPath       string `json:"config_path"`
}

func (s *server) handleAdminConfig(w http.ResponseWriter, r *http.Request) {
	cfg, _ := s.cm.Current()
	if _, ok := s.requireAdmin(w, r, cfg); !ok {
		return
	}
	writeJSON(w, 200, adminConfigResponse{
		SiteName:         cfg.SiteName,
		Rules:            cfg.Rules,
		SessionHours:     cfg.SessionHours,
		MaxUploadMB:      cfg.MaxUploadMB,
		ShowHidden:       cfg.ShowHidden,
		AdminPasswordSet: cfg.AdminPassword != "",
		Version:          s.version,
		Root:             s.rootDir,
		ConfigPath:       s.configPath,
	})
}

type passwordChangeRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

func validPassword(p string) error {
	if len(p) < 6 {
		return errors.New("新密码至少 6 个字符")
	}
	if len(p) > 128 {
		return errors.New("新密码过长")
	}
	for _, r := range p {
		if r < 0x20 || r == 0x7f {
			return errors.New("新密码包含控制字符")
		}
	}
	return nil
}

func (s *server) handleAdminPassword(w http.ResponseWriter, r *http.Request) {
	cfg, _ := s.cm.Current()
	sess, ok := s.requireAdmin(w, r, cfg)
	if !ok {
		return
	}
	ip := clientIP(r, cfg.TrustProxy)
	if allowed, wait := s.limiter.allow(ip); !allowed {
		w.Header().Set("Retry-After", strconvItoa(int(wait.Seconds())+1))
		writeError(w, 429, "rate_limited", "尝试过多，请稍后再试")
		return
	}
	var req passwordChangeRequest
	if !decodeJSON(w, r, &req, 1<<16) {
		return
	}
	if subtle.ConstantTimeCompare([]byte(req.CurrentPassword), []byte(cfg.AdminPassword)) != 1 {
		remaining, banned := s.limiter.fail(ip)
		if banned > 0 {
			writeError(w, 429, "rate_limited", "尝试过多，请稍后再试")
			return
		}
		writeJSON(w, 401, unlockFailure{
			Error:     "forbidden",
			Code:      "forbidden",
			Message:   "当前密码错误",
			Remaining: remaining,
		})
		return
	}
	if req.NewPassword != "" {
		if err := validPassword(req.NewPassword); err != nil {
			writeError(w, 400, "bad_request", err.Error())
			return
		}
	}
	if _, err := s.cm.Update(func(c *Config) error {
		c.AdminPassword = req.NewPassword
		return nil
	}); err != nil {
		slog.Error("保存管理密码失败", "err", err)
		writeError(w, 500, "internal", "配置保存失败")
		return
	}
	s.limiter.reset(ip)
	sess.Admin = adminFingerprint(s.secretBytes(), req.NewPassword)
	s.saveSession(w, sess, cfg.SessionHours, !cfg.InsecureCookie)
	writeJSON(w, 200, map[string]any{
		"ok":                 true,
		"admin_password_set": req.NewPassword != "",
	})
}

type rulesRequest struct {
	Rules []Rule `json:"rules"`
}

func (s *server) handleAdminRules(w http.ResponseWriter, r *http.Request) {
	cfg, _ := s.cm.Current()
	if _, ok := s.requireAdmin(w, r, cfg); !ok {
		return
	}
	var req rulesRequest
	if !decodeJSON(w, r, &req, 1<<20) {
		return
	}
	if req.Rules == nil {
		req.Rules = []Rule{}
	}
	next, err := s.cm.Update(func(c *Config) error {
		c.Rules = req.Rules
		return nil
	})
	if err != nil {
		if errors.Is(err, errRuleOverlap) {
			writeError(w, 400, "rules_overlap", err.Error())
			return
		}
		writeError(w, 400, "bad_request", err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "rules": next.Rules})
}

type settingsRequest struct {
	SiteName     string `json:"site_name"`
	SessionHours int    `json:"session_hours"`
	MaxUploadMB  int64  `json:"max_upload_mb"`
	ShowHidden   bool   `json:"show_hidden"`
}

func (s *server) handleAdminSettings(w http.ResponseWriter, r *http.Request) {
	cfg, _ := s.cm.Current()
	if _, ok := s.requireAdmin(w, r, cfg); !ok {
		return
	}
	var req settingsRequest
	if !decodeJSON(w, r, &req, 1<<16) {
		return
	}
	if _, err := s.cm.Update(func(c *Config) error {
		c.SiteName = req.SiteName
		c.SessionHours = req.SessionHours
		c.MaxUploadMB = req.MaxUploadMB
		c.ShowHidden = req.ShowHidden
		return nil
	}); err != nil {
		writeError(w, 400, "bad_request", err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

type ruleTestRequest struct {
	Path string `json:"path"`
}

func (s *server) handleRuleTest(w http.ResponseWriter, r *http.Request) {
	cfg, rules := s.cm.Current()
	sess, ok := s.requireAdmin(w, r, cfg)
	if !ok {
		return
	}
	var req ruleTestRequest
	if !decodeJSON(w, r, &req, 1<<16) {
		return
	}
	rel, err := cleanRelPath(req.Path)
	if err != nil {
		writeError(w, 400, "bad_path", "路径不合法")
		return
	}
	abs := absPath(rel)
	eff := evaluate(rules, abs)
	admin := s.isAdmin(sess, cfg)
	readable := admin || eff.Password == "" || sess.holds(eff.RuleID)
	writeJSON(w, 200, map[string]any{
		"path":           abs,
		"locked":         eff.Password != "",
		"readable":       readable,
		"rule_index":     eff.RuleIndex,
		"matched_rules":  matchedRuleIndexes(rules, abs),
		"requires_admin": false,
	})
}

func (s *server) handleAdminStatus(w http.ResponseWriter, r *http.Request) {
	cfg, _ := s.cm.Current()
	if _, ok := s.requireAdmin(w, r, cfg); !ok {
		return
	}
	var free uint64
	var st syscall.Statfs_t
	if err := syscall.Statfs(s.rootDir, &st); err == nil {
		free = uint64(st.Bavail) * uint64(st.Bsize)
	}
	writeJSON(w, 200, map[string]any{
		"version":       s.version,
		"root":          s.rootDir,
		"config_path":   s.configPath,
		"uptime_sec":    int64(time.Since(s.start).Seconds()),
		"rules":         len(cfg.Rules),
		"disk_free":     free,
		"session_hours": cfg.SessionHours,
	})
}

func (s *server) handleLogoutAll(w http.ResponseWriter, r *http.Request) {
	cfg, _ := s.cm.Current()
	if _, ok := s.requireAdmin(w, r, cfg); !ok {
		return
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		writeError(w, 500, "internal", "生成密钥失败")
		return
	}
	tmp := s.secretPath + ".tmp"
	if err := os.WriteFile(tmp, secret, 0o600); err != nil {
		writeError(w, 500, "internal", "写入密钥失败")
		return
	}
	if err := os.Rename(tmp, s.secretPath); err != nil {
		os.Remove(tmp)
		writeError(w, 500, "internal", "写入密钥失败")
		return
	}
	_ = os.Chmod(s.secretPath, 0o600)
	if err := s.cm.SetSecret(secret); err != nil {
		writeError(w, 500, "internal", err.Error())
		return
	}
	s.secret.Store(secret)
	s.clearSession(w, !cfg.InsecureCookie)
	writeJSON(w, 200, map[string]any{"ok": true})
}
