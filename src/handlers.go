package main

import (
	"archive/zip"
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

//go:embed static
var embeddedStatic embed.FS

type server struct {
	cm         *configManager
	files      *fileSystem
	limiter    *limiter
	version    string
	rootDir    string
	configPath string
	secretPath string
	start      time.Time
	secret     atomic.Value // []byte
}

func (s *server) secretBytes() []byte { return s.secret.Load().([]byte) }

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("GET /admin", s.handleAdminPage)
	mux.HandleFunc("GET /favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	sub, err := fs.Sub(embeddedStatic, "static")
	if err != nil {
		panic(err)
	}
	mux.Handle("GET /static/", http.StripPrefix("/static/", cacheStatic(http.FileServerFS(sub))))

	mux.HandleFunc("GET /api/session", s.handleSession)
	mux.HandleFunc("POST /api/unlock", s.handleUnlock)
	mux.HandleFunc("POST /api/logout", s.handleLogout)

	mux.HandleFunc("GET /api/list", s.handleList)
	mux.HandleFunc("GET /api/rule-password", s.handleRulePassword)
	mux.HandleFunc("GET /api/download", s.handleDownload)
	mux.HandleFunc("GET /api/zip", s.handleZip)
	mux.HandleFunc("POST /api/stat", s.handleStat)
	mux.HandleFunc("PUT /api/file", s.handleUpload)
	mux.HandleFunc("POST /api/mkdir", s.handleMkdir)
	mux.HandleFunc("POST /api/rename", s.handleRename)
	mux.HandleFunc("POST /api/move", s.handleMove)
	mux.HandleFunc("POST /api/delete", s.handleDelete)
	mux.HandleFunc("POST /api/undo-delete", s.handleUndoDelete)

	mux.HandleFunc("GET /api/admin/config", s.handleAdminConfig)
	mux.HandleFunc("POST /api/admin/password", s.handleAdminPassword)
	mux.HandleFunc("PUT /api/admin/rules", s.handleAdminRules)
	mux.HandleFunc("PUT /api/admin/settings", s.handleAdminSettings)
	mux.HandleFunc("POST /api/admin/rule-test", s.handleRuleTest)
	mux.HandleFunc("GET /api/admin/status", s.handleAdminStatus)
	mux.HandleFunc("POST /api/admin/logout-all", s.handleLogoutAll)

	return s.middleware(mux)
}

func cacheStatic(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Revalidate on every load: the binary can be replaced without notice,
		// and a 304 is cheaper than serving a stale UI.
		w.Header().Set("Cache-Control", "no-cache")
		h.ServeHTTP(w, r)
	})
}

func (s *server) handleIndex(w http.ResponseWriter, r *http.Request) {
	data, err := embeddedStatic.ReadFile("static/index.html")
	if err != nil {
		writeError(w, 500, "internal", "failed to load the page")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(data)
}

func (s *server) handleAdminPage(w http.ResponseWriter, r *http.Request) {
	data, err := embeddedStatic.ReadFile("static/admin.html")
	if err != nil {
		writeError(w, 500, "internal", "failed to load the page")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(data)
}

func isUnsafeMethod(m string) bool {
	switch m {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

func setSecurityHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Frame-Options", "SAMEORIGIN")
	h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self'; object-src 'none'; frame-ancestors 'self'")
}

func (s *server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		setSecurityHeaders(sw)

		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("panic", "err", rec, "path", r.URL.Path)
				if sw.status == 0 {
					writeError(sw, http.StatusInternalServerError, "internal", "server error")
				}
			}
			s.logRequest(sw, r, start)
		}()

		if isUnsafeMethod(r.Method) {
			if r.Header.Get("X-Requested-With") != "tinyhutch" {
				writeError(sw, 403, "forbidden", "missing request header")
				return
			}
			if origin := r.Header.Get("Origin"); origin != "" {
				u, err := url.Parse(origin)
				if err != nil || !strings.EqualFold(u.Host, r.Host) {
					writeError(sw, 403, "forbidden", "cross-site request rejected")
					return
				}
			}
		}
		next.ServeHTTP(sw, r)
	})
}

// logRequest keeps the default log quiet: successful reads (including static
// assets) are debug-only; mutations and 4xx/5xx are always logged.
func (s *server) logRequest(sw *statusWriter, r *http.Request, start time.Time) {
	if sw.status == 0 {
		sw.status = http.StatusOK
	}
	if strings.HasPrefix(r.URL.Path, "/static/") && sw.status < 400 {
		return
	}
	level := slog.LevelDebug
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		level = slog.LevelInfo
	}
	if sw.status >= 500 {
		level = slog.LevelError
	} else if sw.status >= 400 {
		level = slog.LevelInfo
	}
	if !slog.Default().Enabled(context.Background(), level) {
		return
	}
	cfg, _ := s.cm.Current()
	sess := s.loadSession(r)
	slog.Log(context.Background(), level, "request",
		"method", r.Method,
		"path", r.URL.Path,
		"status", sw.status,
		"bytes", sw.bytes,
		"dur", time.Since(start).Round(time.Millisecond),
		"ip", clientIP(r, cfg.TrustProxy),
		"admin", sess.isAdmin(s.secretBytes(), cfg.AdminPassword),
	)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any, max int64) bool {
	r.Body = http.MaxBytesReader(w, r.Body, max)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, 400, "bad_request", "invalid request body")
		return false
	}
	return true
}

func (s *server) isAdmin(sess *session, cfg *Config) bool {
	return sess.isAdmin(s.secretBytes(), cfg.AdminPassword)
}

func (s *server) canRead(sess *session, cfg *Config, eff Effective) bool {
	if s.isAdmin(sess, cfg) {
		return true
	}
	if eff.Password == "" {
		return true
	}
	return sess.holds(eff.RuleID)
}

func (s *server) requireAdmin(w http.ResponseWriter, r *http.Request, cfg *Config) (*session, bool) {
	sess := s.loadSession(r)
	if !s.isAdmin(sess, cfg) {
		writeError(w, 403, "forbidden", "admin login required")
		return nil, false
	}
	return sess, true
}

func fileError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errBadPath):
		writeError(w, 400, "bad_path", "invalid path")
	case errors.Is(err, errNotFound), errors.Is(err, fs.ErrNotExist):
		writeError(w, 404, "not_found", "not found")
	case errors.Is(err, errExists), errors.Is(err, fs.ErrExist):
		writeError(w, 409, "exists", "a file or folder with that name already exists")
	case errors.Is(err, errIsDir):
		writeError(w, 409, "exists", "the target is a directory")
	case errors.Is(err, errTooLarge):
		writeError(w, 413, "too_large", "file exceeds the upload limit")
	case errors.Is(err, syscall.ENOSPC):
		writeError(w, 507, "no_space", "not enough disk space")
	case errors.Is(err, syscall.ENOTEMPTY):
		writeError(w, 409, "exists", "the directory is not empty")
	case errors.Is(err, syscall.EXDEV):
		writeError(w, 409, "cross_device", "cannot move across filesystems")
	default:
		slog.Error("filesystem error", "err", err)
		writeError(w, 500, "internal", "server error")
	}
}

func (s *server) handleSession(w http.ResponseWriter, r *http.Request) {
	cfg, _ := s.cm.Current()
	sess := s.loadSession(r)
	writeJSON(w, 200, map[string]any{
		"admin":              s.isAdmin(sess, cfg),
		"admin_password_set": cfg.AdminPassword != "",
		"unlocked":           len(sess.Unlocked),
		"expires":            sess.Exp,
		"site_name":          cfg.SiteName,
	})
}

type unlockRequest struct {
	Path     string `json:"path"`
	Password string `json:"password"`
}

type unlockResponse struct {
	Admin    bool   `json:"admin"`
	Unlocked bool   `json:"unlocked"`
	Path     string `json:"path"`
}

type unlockFailure struct {
	Error     string `json:"error"`
	Code      string `json:"code"`
	Message   string `json:"message"`
	Remaining int    `json:"remaining,omitempty"`
}

func (s *server) handleUnlock(w http.ResponseWriter, r *http.Request) {
	cfg, rules := s.cm.Current()
	ip := clientIP(r, cfg.TrustProxy)
	if ok, wait := s.limiter.allow(ip); !ok {
		w.Header().Set("Retry-After", strconvItoa(int(wait.Seconds())+1))
		writeError(w, 429, "rate_limited", "too many attempts, try again later")
		return
	}
	var req unlockRequest
	if !decodeJSON(w, r, &req, 8192) {
		return
	}
	rel, err := cleanRelPath(req.Path)
	if err != nil {
		writeError(w, 400, "bad_path", "invalid path")
		return
	}
	abs := absPath(rel)
	eff := evaluate(rules, abs)
	sess := s.loadSession(r)

	adminOK := subtle.ConstantTimeCompare([]byte(req.Password), []byte(cfg.AdminPassword)) == 1
	lockOK := eff.Password != "" &&
		subtle.ConstantTimeCompare([]byte(req.Password), []byte(eff.Password)) == 1

	if adminOK || lockOK {
		s.limiter.reset(ip)
		if adminOK {
			sess.Admin = adminFingerprint(s.secretBytes(), req.Password)
		}
		if lockOK {
			sess.grant(eff.RuleID)
		}
		s.saveSession(w, sess, cfg.SessionHours, !cfg.InsecureCookie)
		writeJSON(w, 200, unlockResponse{Admin: adminOK, Unlocked: lockOK, Path: abs})
		return
	}

	remaining, banned := s.limiter.fail(ip)
	if banned > 0 {
		w.Header().Set("Retry-After", strconvItoa(int(limiterBan.Seconds())))
		writeError(w, 429, "rate_limited", "too many attempts, try again later")
		return
	}
	writeJSON(w, 401, unlockFailure{
		Error:     "locked",
		Code:      "locked",
		Message:   "wrong password",
		Remaining: remaining,
	})
}

func (s *server) handleLogout(w http.ResponseWriter, r *http.Request) {
	cfg, _ := s.cm.Current()
	s.clearSession(w, !cfg.InsecureCookie)
	writeJSON(w, 200, map[string]any{"ok": true})
}

type listEntry struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	Size      int64  `json:"size"`
	ModTime   string `json:"mtime"`
	Locked    bool   `json:"locked,omitempty"`
	Protected bool   `json:"protected,omitempty"`
	// Inherited means the lock comes from a parent directory rule. Such
	// entries cannot be locked or unlocked on their own.
	Inherited bool `json:"inherited,omitempty"`
}

type listResponse struct {
	Path      string      `json:"path"`
	Parent    string      `json:"parent"`
	Admin     bool        `json:"admin"`
	Truncated bool        `json:"truncated"`
	Entries   []listEntry `json:"entries"`
}

func (s *server) handleList(w http.ResponseWriter, r *http.Request) {
	cfg, rules := s.cm.Current()
	sess := s.loadSession(r)
	rel, err := cleanRelPath(r.URL.Query().Get("path"))
	if err != nil {
		writeError(w, 400, "bad_path", "invalid path")
		return
	}
	if err := s.files.guard(rel, cfg.ShowHidden, false); err != nil {
		fileError(w, err)
		return
	}
	info, err := s.files.stat(rel)
	if err != nil {
		fileError(w, err)
		return
	}
	if !info.IsDir() {
		writeError(w, 400, "bad_path", "not a directory")
		return
	}
	if eff := evaluate(rules, absPath(rel)); !s.canRead(sess, cfg, eff) {
		writeError(w, 401, "locked", "this path requires a password")
		return
	}
	entries, truncated, err := s.files.list(rel, cfg.ShowHidden)
	if err != nil {
		fileError(w, err)
		return
	}
	admin := s.isAdmin(sess, cfg)
	out := make([]listEntry, 0, len(entries))
	for _, e := range entries {
		child := joinRel(rel, e.Name)
		childAbs := absPath(child)
		eff := evaluate(rules, childAbs)
		protected := eff.Password != ""
		locked := protected && !admin && !sess.holds(eff.RuleID)
		typ := "file"
		if e.IsDir {
			typ = "dir"
		}
		out = append(out, listEntry{
			Name:      e.Name,
			Type:      typ,
			Size:      e.Size,
			ModTime:   e.ModTime.Format(time.RFC3339),
			Locked:    locked,
			Protected: protected,
			Inherited: protected && eff.RulePath != childAbs,
		})
	}
	writeJSON(w, 200, listResponse{
		Path:      absPath(rel),
		Parent:    absPath(parentOf(rel)),
		Admin:     admin,
		Truncated: truncated,
		Entries:   out,
	})
}

// authorizeDownload allows access through an unlocked session or, for locked
// files, through the password query parameter. The same rate limiter as
// /api/unlock is used so scripted downloads cannot be brute-forced.
func (s *server) authorizeDownload(w http.ResponseWriter, r *http.Request, cfg *Config, sess *session, eff Effective) bool {
	if s.canRead(sess, cfg, eff) {
		return true
	}
	if eff.Password == "" {
		return false
	}
	pw := r.URL.Query().Get("password")
	if pw == "" {
		writeError(w, 401, "locked", "this file requires a password")
		return false
	}
	ip := clientIP(r, cfg.TrustProxy)
	if ok, wait := s.limiter.allow(ip); !ok {
		w.Header().Set("Retry-After", strconvItoa(int(wait.Seconds())+1))
		writeError(w, 429, "rate_limited", "too many attempts, try again later")
		return false
	}
	if subtle.ConstantTimeCompare([]byte(pw), []byte(eff.Password)) != 1 {
		remaining, banned := s.limiter.fail(ip)
		if banned > 0 {
			w.Header().Set("Retry-After", strconvItoa(int(limiterBan.Seconds())))
			writeError(w, 429, "rate_limited", "too many attempts, try again later")
			return false
		}
		writeJSON(w, 401, unlockFailure{
			Error:     "locked",
			Code:      "locked",
			Message:   "wrong password",
			Remaining: remaining,
		})
		return false
	}
	s.limiter.reset(ip)
	return true
}

// handleRulePassword returns the effective rule password for a file so the
// frontend can build a direct download link. It only answers when the current
// session can already read the path (admin or unlocked rule); it never accepts
// a password guess here, so /api/unlock remains the only rate-limited check.
func (s *server) handleRulePassword(w http.ResponseWriter, r *http.Request) {
	cfg, rules := s.cm.Current()
	sess := s.loadSession(r)
	rel, err := cleanRelPath(r.URL.Query().Get("path"))
	if err != nil || rel == "" {
		writeError(w, 400, "bad_path", "invalid path")
		return
	}
	if err := s.files.guard(rel, cfg.ShowHidden, false); err != nil {
		fileError(w, err)
		return
	}
	eff := evaluate(rules, absPath(rel))
	if !s.canRead(sess, cfg, eff) {
		writeError(w, 401, "locked", "this file requires a password")
		return
	}
	info, err := s.files.stat(rel)
	if err != nil {
		fileError(w, err)
		return
	}
	if info.IsDir() || !info.Mode().IsRegular() {
		writeError(w, 400, "bad_path", "not a file")
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, 200, map[string]string{"password": eff.Password})
}

func (s *server) handleDownload(w http.ResponseWriter, r *http.Request) {
	cfg, rules := s.cm.Current()
	sess := s.loadSession(r)
	rel, err := cleanRelPath(r.URL.Query().Get("path"))
	if err != nil || rel == "" {
		writeError(w, 400, "bad_path", "invalid path")
		return
	}
	if err := s.files.guard(rel, cfg.ShowHidden, false); err != nil {
		fileError(w, err)
		return
	}
	if !s.authorizeDownload(w, r, cfg, sess, evaluate(rules, absPath(rel))) {
		return
	}
	file, info, err := s.files.open(rel)
	if err != nil {
		fileError(w, err)
		return
	}
	defer file.Close()
	if info.IsDir() || !info.Mode().IsRegular() {
		writeError(w, 400, "bad_path", "not a file")
		return
	}
	name := path.Base(rel)
	if ctype := contentTypeFor(name); ctype != "" {
		w.Header().Set("Content-Type", ctype)
	}
	if cd := mime.FormatMediaType("attachment", map[string]string{"filename": name}); cd != "" {
		w.Header().Set("Content-Disposition", cd)
	}
	w.Header().Set("Cache-Control", "private, no-store")
	http.ServeContent(w, r, name, info.ModTime(), file)
}

func contentTypeFor(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".avif":
		return "image/avif"
	case ".txt", ".log", ".md", ".csv", ".json", ".yml", ".yaml", ".ini", ".conf":
		return "text/plain; charset=utf-8"
	case ".pdf":
		return "application/pdf"
	case ".mp3":
		return "audio/mpeg"
	case ".wav":
		return "audio/wav"
	case ".ogg", ".oga":
		return "audio/ogg"
	case ".m4a":
		return "audio/mp4"
	case ".flac":
		return "audio/flac"
	case ".mp4", ".m4v":
		return "video/mp4"
	case ".webm":
		return "video/webm"
	case ".ogv":
		return "video/ogg"
	case ".html", ".htm":
		return "text/html; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".xml":
		return "application/xml"
	case ".js":
		return "text/javascript; charset=utf-8"
	}
	return ""
}

type statRequest struct {
	Paths []string `json:"paths"`
}

// handleStat returns the recursive file count and byte size for a selection.
func (s *server) handleStat(w http.ResponseWriter, r *http.Request) {
	cfg, _ := s.cm.Current()
	if _, ok := s.requireAdmin(w, r, cfg); !ok {
		return
	}
	var req statRequest
	if !decodeJSON(w, r, &req, 1<<20) {
		return
	}
	seen := make(map[string]bool)
	var rels []string
	for _, p := range req.Paths {
		rel, err := cleanRelPath(p)
		if err != nil || rel == "" || seen[rel] {
			continue
		}
		if err := s.files.guard(rel, cfg.ShowHidden, false); err != nil {
			continue
		}
		seen[rel] = true
		rels = append(rels, rel)
	}
	rels = compactRelPaths(rels)
	if len(rels) == 0 {
		writeError(w, 400, "bad_path", "no paths to measure")
		return
	}
	files := 0
	var bytes int64
	fsys := s.files.root.FS()
	for _, rel := range rels {
		_ = fs.WalkDir(fsys, rel, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.Type()&fs.ModeSymlink != 0 {
				return nil
			}
			if err := s.files.guard(p, cfg.ShowHidden, false); err != nil {
				return nil
			}
			if d.IsDir() {
				return nil
			}
			info, err := d.Info()
			if err != nil || !info.Mode().IsRegular() {
				return nil
			}
			files++
			bytes += info.Size()
			return nil
		})
	}
	writeJSON(w, 200, map[string]any{"files": files, "bytes": bytes})
}

func compactRelPaths(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		nested := false
		for _, q := range paths {
			if p != q && strings.HasPrefix(p, q+"/") {
				nested = true
				break
			}
		}
		if !nested {
			out = append(out, p)
		}
	}
	return out
}

// handleZip streams the selected files and directories as a zip archive.
func (s *server) handleZip(w http.ResponseWriter, r *http.Request) {
	cfg, _ := s.cm.Current()
	sess := s.loadSession(r)
	if !s.isAdmin(sess, cfg) {
		writeError(w, 403, "forbidden", "admin login required")
		return
	}
	raw := r.URL.Query()["path"]
	if len(raw) == 0 {
		writeError(w, 400, "bad_path", "no files selected")
		return
	}
	seen := make(map[string]bool)
	items := make([]string, 0, len(raw))
	for _, p := range raw {
		rel, err := cleanRelPath(p)
		if err != nil || rel == "" || seen[rel] {
			continue
		}
		if err := s.files.guard(rel, cfg.ShowHidden, false); err != nil {
			continue
		}
		seen[rel] = true
		items = append(items, rel)
	}
	items = compactRelPaths(items)
	if len(items) == 0 {
		writeError(w, 404, "not_found", "no files to download")
		return
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": "tinyhutch.zip"}))
	w.Header().Set("Cache-Control", "no-store")
	zw := zip.NewWriter(w)
	defer zw.Close()

	fsys := s.files.root.FS()
	for _, rel := range items {
		_ = fs.WalkDir(fsys, rel, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.Type()&fs.ModeSymlink != 0 {
				return nil
			}
			if err := s.files.guard(p, cfg.ShowHidden, false); err != nil {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			name := p
			if d.IsDir() {
				hdr, err := zip.FileInfoHeader(info)
				if err != nil {
					return nil
				}
				hdr.Name = name + "/"
				hdr.Method = zip.Store
				_, _ = zw.CreateHeader(hdr)
				return nil
			}
			if !info.Mode().IsRegular() {
				return nil
			}
			hdr, err := zip.FileInfoHeader(info)
			if err != nil {
				return nil
			}
			hdr.Name = name
			hdr.Method = zip.Deflate
			dst, err := zw.CreateHeader(hdr)
			if err != nil {
				return nil
			}
			src, err := s.files.root.Open(p)
			if err != nil {
				return nil
			}
			defer src.Close()
			_, _ = io.Copy(dst, src)
			return nil
		})
	}
}

func (s *server) handleUpload(w http.ResponseWriter, r *http.Request) {
	cfg, _ := s.cm.Current()
	if _, ok := s.requireAdmin(w, r, cfg); !ok {
		return
	}
	rel, err := cleanRelPath(r.URL.Query().Get("path"))
	if err != nil {
		writeError(w, 400, "bad_path", "invalid path")
		return
	}
	name := r.URL.Query().Get("name")
	if err := validateName(name, cfg.ShowHidden); err != nil {
		writeError(w, 400, "bad_name", "invalid file name")
		return
	}
	if err := s.files.guard(rel, cfg.ShowHidden, false); err != nil {
		fileError(w, err)
		return
	}
	dirInfo, err := s.files.stat(rel)
	if err != nil {
		fileError(w, err)
		return
	}
	if !dirInfo.IsDir() {
		writeError(w, 400, "bad_path", "the target is not a directory")
		return
	}
	target := joinRel(rel, name)
	if err := s.files.guard(target, cfg.ShowHidden, true); err != nil {
		fileError(w, err)
		return
	}
	maxBytes := cfg.maxUploadBytes()
	if r.ContentLength > maxBytes {
		writeError(w, 413, "too_large", "file exceeds the upload limit")
		return
	}
	overwrite := r.URL.Query().Get("overwrite") == "1"
	n, err := s.files.saveUpload(rel, name, overwrite, maxBytes, r.Body)
	if err != nil {
		fileError(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"ok": true, "name": name, "size": n})
}

func validateName(name string, showHidden bool) error {
	if name == "" || name == "." || name == ".." {
		return errBadPath
	}
	if len(name) > 255 {
		return errBadPath
	}
	if strings.ContainsAny(name, "/\\\x00") {
		return errBadPath
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return errBadPath
		}
	}
	if !showHidden && strings.HasPrefix(name, ".") {
		return errBadPath
	}
	return nil
}

type pathNameRequest struct {
	Path string `json:"path"`
	Name string `json:"name"`
}

func (s *server) handleMkdir(w http.ResponseWriter, r *http.Request) {
	cfg, _ := s.cm.Current()
	if _, ok := s.requireAdmin(w, r, cfg); !ok {
		return
	}
	var req pathNameRequest
	if !decodeJSON(w, r, &req, 1<<16) {
		return
	}
	if err := validateName(req.Name, cfg.ShowHidden); err != nil {
		writeError(w, 400, "bad_name", "invalid folder name")
		return
	}
	rel, err := cleanRelPath(req.Path)
	if err != nil {
		writeError(w, 400, "bad_path", "invalid path")
		return
	}
	if err := s.files.guard(rel, cfg.ShowHidden, false); err != nil {
		fileError(w, err)
		return
	}
	target := joinRel(rel, req.Name)
	if err := s.files.guard(target, cfg.ShowHidden, true); err != nil {
		fileError(w, err)
		return
	}
	if _, err := s.files.stat(target); err == nil {
		writeError(w, 409, "exists", "a file or folder with that name already exists")
		return
	}
	if err := s.files.mkdir(target); err != nil {
		fileError(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"ok": true, "path": absPath(target)})
}

func (s *server) handleRename(w http.ResponseWriter, r *http.Request) {
	cfg, _ := s.cm.Current()
	if _, ok := s.requireAdmin(w, r, cfg); !ok {
		return
	}
	var req pathNameRequest
	if !decodeJSON(w, r, &req, 1<<16) {
		return
	}
	if err := validateName(req.Name, cfg.ShowHidden); err != nil {
		writeError(w, 400, "bad_name", "invalid name")
		return
	}
	from, err := cleanRelPath(req.Path)
	if err != nil || from == "" {
		writeError(w, 400, "bad_path", "invalid path")
		return
	}
	if err := s.files.guard(from, cfg.ShowHidden, false); err != nil {
		fileError(w, err)
		return
	}
	to := joinRel(parentOf(from), req.Name)
	if err := s.files.guard(to, cfg.ShowHidden, true); err != nil {
		fileError(w, err)
		return
	}
	if _, err := s.files.stat(to); err == nil {
		writeError(w, 409, "exists", "a file or folder with that name already exists")
		return
	}
	if err := s.validateRulesAfterRename(from, to); err != nil {
		writeError(w, 409, "rules_move_conflict", "this move would create overlapping lock rules; adjust the rules first")
		return
	}
	if err := s.files.rename(from, to); err != nil {
		fileError(w, err)
		return
	}
	s.syncRulesRename(from, to)
	writeJSON(w, 200, map[string]any{"ok": true, "path": absPath(to)})
}

type deleteRequest struct {
	Path      string   `json:"path"`
	Paths     []string `json:"paths"`
	Recursive bool     `json:"recursive"`
}

type trashEntry struct {
	Trash    string `json:"trash"`
	Original string `json:"original"`
}

type trashManifest struct {
	Entries []trashEntry `json:"entries"`
	Rules   []Rule       `json:"rules,omitempty"`
}

type undoDeleteRequest struct {
	Token string `json:"token"`
}

type moveRequest struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// handleMove moves a file or directory inside the root. Both paths are
// relative to the root; the target must not exist.
func (s *server) handleMove(w http.ResponseWriter, r *http.Request) {
	cfg, _ := s.cm.Current()
	if _, ok := s.requireAdmin(w, r, cfg); !ok {
		return
	}
	var req moveRequest
	if !decodeJSON(w, r, &req, 1<<16) {
		return
	}
	from, err := cleanRelPath(req.From)
	if err != nil || from == "" {
		writeError(w, 400, "bad_path", "invalid path")
		return
	}
	to, err := cleanRelPath(req.To)
	if err != nil || to == "" {
		writeError(w, 400, "bad_path", "invalid path")
		return
	}
	if from == to {
		writeJSON(w, 200, map[string]any{"ok": true, "path": absPath(to)})
		return
	}
	if err := s.files.guard(from, cfg.ShowHidden, false); err != nil {
		fileError(w, err)
		return
	}
	if err := s.files.guard(to, cfg.ShowHidden, true); err != nil {
		fileError(w, err)
		return
	}
	info, err := s.files.stat(from)
	if err != nil {
		fileError(w, err)
		return
	}
	if info.IsDir() && strings.HasPrefix(to, from+"/") {
		writeError(w, 400, "bad_path", "cannot move into itself or a subfolder")
		return
	}
	parent := parentOf(to)
	parentInfo, err := s.files.stat(parent)
	if err != nil {
		fileError(w, err)
		return
	}
	if !parentInfo.IsDir() {
		writeError(w, 400, "bad_path", "the target is not a directory")
		return
	}
	if _, err := s.files.stat(to); err == nil {
		writeError(w, 409, "exists", "a file or folder with that name already exists")
		return
	}
	if err := s.validateRulesAfterRename(from, to); err != nil {
		writeError(w, 409, "rules_move_conflict", "this move would create overlapping lock rules; adjust the rules first")
		return
	}
	if err := s.files.rename(from, to); err != nil {
		fileError(w, err)
		return
	}
	s.syncRulesRename(from, to)
	writeJSON(w, 200, map[string]any{"ok": true, "path": absPath(to)})
}

func (s *server) handleDelete(w http.ResponseWriter, r *http.Request) {
	cfg, _ := s.cm.Current()
	if _, ok := s.requireAdmin(w, r, cfg); !ok {
		return
	}
	var req deleteRequest
	if !decodeJSON(w, r, &req, 1<<20) {
		return
	}
	raw := req.Paths
	if len(raw) == 0 && req.Path != "" {
		raw = []string{req.Path}
	}
	if len(raw) == 0 {
		writeError(w, 400, "bad_path", "no files selected")
		return
	}
	seen := make(map[string]bool)
	var rels []string
	for _, p := range raw {
		rel, err := cleanRelPath(p)
		if err != nil || rel == "" || seen[rel] {
			continue
		}
		if err := s.files.guard(rel, cfg.ShowHidden, false); err != nil {
			fileError(w, err)
			return
		}
		seen[rel] = true
		rels = append(rels, rel)
	}
	rels = compactRelPaths(rels)
	if len(rels) == 0 {
		writeError(w, 400, "bad_path", "no paths to delete")
		return
	}

	if err := s.files.root.MkdirAll(trashDir, 0o700); err != nil {
		fileError(w, err)
		return
	}
	token, err := randomHex(8)
	if err != nil {
		writeError(w, 500, "internal", "failed to generate the undo token")
		return
	}
	sess := path.Join(trashDir, token)
	if err := s.files.root.MkdirAll(sess, 0o700); err != nil {
		fileError(w, err)
		return
	}

	var entries []trashEntry
	rollback := func() {
		for _, e := range entries {
			_ = s.files.root.Rename(path.Join(sess, e.Trash), e.Original)
		}
		_ = s.files.root.RemoveAll(sess)
	}
	for i, rel := range rels {
		name := strconv.Itoa(i)
		if err := s.files.root.Rename(rel, path.Join(sess, name)); err != nil {
			rollback()
			fileError(w, err)
			return
		}
		entries = append(entries, trashEntry{Trash: name, Original: rel})
	}

	rules := s.takeExactRules(rels)
	manifest := trashManifest{Entries: entries, Rules: rules}
	data, err := json.Marshal(manifest)
	if err != nil {
		rollback()
		s.restoreRules(rules)
		writeError(w, 500, "internal", "failed to save the undo information")
		return
	}
	if err := s.files.root.WriteFile(path.Join(sess, "manifest.json"), data, 0o600); err != nil {
		rollback()
		s.restoreRules(rules)
		fileError(w, err)
		return
	}
	time.AfterFunc(60*time.Second, func() {
		_ = s.files.root.RemoveAll(sess)
	})

	writeJSON(w, 200, map[string]any{"ok": true, "undo": token, "count": len(entries)})
}

func (s *server) handleUndoDelete(w http.ResponseWriter, r *http.Request) {
	cfg, _ := s.cm.Current()
	if _, ok := s.requireAdmin(w, r, cfg); !ok {
		return
	}
	var req undoDeleteRequest
	if !decodeJSON(w, r, &req, 1<<16) {
		return
	}
	if !validTrashToken(req.Token) {
		writeError(w, 400, "bad_path", "invalid undo token")
		return
	}
	sess := path.Join(trashDir, req.Token)
	data, err := s.files.root.ReadFile(path.Join(sess, "manifest.json"))
	if err != nil {
		writeError(w, 404, "not_found", "undo has expired")
		return
	}
	var m trashManifest
	if err := json.Unmarshal(data, &m); err != nil {
		writeError(w, 500, "internal", "undo information is corrupted")
		return
	}
	for _, e := range m.Entries {
		if err := s.files.guard(e.Original, cfg.ShowHidden, true); err != nil {
			fileError(w, err)
			return
		}
		if _, err := s.files.root.Lstat(e.Original); err == nil {
			writeError(w, 409, "exists", "a file with that name is already in the original location")
			return
		}
		if err := s.files.root.Rename(path.Join(sess, e.Trash), e.Original); err != nil {
			fileError(w, err)
			return
		}
	}
	s.restoreRules(m.Rules)
	_ = s.files.root.RemoveAll(sess)
	writeJSON(w, 200, map[string]any{"ok": true})
}

func validTrashToken(token string) bool {
	if len(token) != 16 {
		return false
	}
	for _, c := range token {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// takeExactRules removes exact-path rules for the deleted paths and returns
// them so a later undo can restore the locks.
func (s *server) takeExactRules(rels []string) []Rule {
	var removed []Rule
	_, err := s.cm.Update(func(c *Config) error {
		out := c.Rules[:0]
		for _, r := range c.Rules {
			if isExactPath(r.Path) && ruleUnder(r.Path, rels) {
				removed = append(removed, r)
				continue
			}
			out = append(out, r)
		}
		c.Rules = out
		return nil
	})
	if err != nil {
		slog.Warn("failed to clean up rules while deleting a path", "err", err)
		return nil
	}
	return removed
}

func ruleUnder(rulePath string, rels []string) bool {
	for _, rel := range rels {
		abs := absPath(rel)
		if rulePath == abs || strings.HasPrefix(rulePath, abs+"/") {
			return true
		}
	}
	return false
}

func (s *server) restoreRules(rules []Rule) {
	if len(rules) == 0 {
		return
	}
	_, err := s.cm.Update(func(c *Config) error {
		for _, r := range rules {
			exists := false
			for _, x := range c.Rules {
				if x.Path == r.Path && x.Mode == r.Mode && x.Password == r.Password {
					exists = true
					break
				}
			}
			if !exists {
				c.Rules = append(c.Rules, r)
			}
		}
		return nil
	})
	if err != nil {
		slog.Warn("failed to restore rules while undoing a delete", "err", err)
	}
}

func shiftedRules(rules []Rule, from, to string) []Rule {
	fromAbs, toAbs := absPath(from), absPath(to)
	out := append([]Rule(nil), rules...)
	for i := range out {
		switch {
		case out[i].Path == fromAbs:
			out[i].Path = toAbs
		case strings.HasPrefix(out[i].Path, fromAbs+"/"):
			out[i].Path = toAbs + strings.TrimPrefix(out[i].Path, fromAbs)
		}
	}
	return out
}

// validateRulesAfterRename refuses a move/rename that would leave two lock
// rules containing each other (for example moving a locked folder into another
// locked folder).
func (s *server) validateRulesAfterRename(from, to string) error {
	cfg, _ := s.cm.Current()
	_, err := compileRules(shiftedRules(cfg.Rules, from, to), []byte("validation"))
	return err
}

// syncRulesRename keeps exact-path rules attached to renamed or moved files.
func (s *server) syncRulesRename(from, to string) {
	_, err := s.cm.Update(func(c *Config) error {
		c.Rules = shiftedRules(c.Rules, from, to)
		return nil
	})
	if err != nil {
		slog.Warn("failed to sync rules after rename", "err", err)
	}
}

// syncRulesDelete removes exact-path rules for deleted paths.
func (s *server) syncRulesDelete(rel string) {
	abs := absPath(rel)
	_, err := s.cm.Update(func(c *Config) error {
		out := c.Rules[:0]
		for _, r := range c.Rules {
			if isExactPath(r.Path) && (r.Path == abs || strings.HasPrefix(r.Path, abs+"/")) {
				continue
			}
			out = append(out, r)
		}
		c.Rules = out
		return nil
	})
	if err != nil {
		slog.Warn("failed to remove rules for a deleted path", "err", err)
	}
}

func strconvItoa(v int) string {
	if v <= 0 {
		return "0"
	}
	var buf [24]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
