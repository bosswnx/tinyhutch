package main

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultSessionHours = 12
	defaultMaxUploadMB  = 4096
	maxRules            = 1000
)

// Config mirrors config.json.
type Config struct {
	SiteName       string `json:"site_name"`
	ListenHost     string `json:"listen_host"`
	ListenPort     int    `json:"listen_port"`
	AdminPassword  string `json:"admin_password"`
	SessionHours   int    `json:"session_hours"`
	MaxUploadMB    int64  `json:"max_upload_mb"`
	ShowHidden     bool   `json:"show_hidden"`
	LogLevel       string `json:"log_level"`
	TrustProxy     bool   `json:"trust_proxy"`
	InsecureCookie bool   `json:"insecure_cookie"`
	Rules          []Rule `json:"rules"`
}

// ListenAddr joins listen_host and listen_port into a net.Listen address.
func (c *Config) ListenAddr() string {
	return net.JoinHostPort(c.ListenHost, strconv.Itoa(c.ListenPort))
}

func (c *Config) Clone() *Config {
	n := *c
	n.Rules = append([]Rule(nil), c.Rules...)
	if n.Rules == nil {
		n.Rules = []Rule{}
	}
	return &n
}

func (c *Config) maxUploadBytes() int64 { return c.MaxUploadMB << 20 }

func (c *Config) sessionDuration() time.Duration {
	return time.Duration(c.SessionHours) * time.Hour
}

func applyDefaults(c *Config) {
	if c.SiteName == "" {
		c.SiteName = "TinyHutch"
	}
	if c.ListenHost == "" {
		c.ListenHost = "127.0.0.1"
	}
	if c.ListenPort == 0 {
		c.ListenPort = 7500
	}
	if c.LogLevel == "" {
		c.LogLevel = "info"
	}
	if c.SessionHours == 0 {
		c.SessionHours = defaultSessionHours
	}
	if c.MaxUploadMB == 0 {
		c.MaxUploadMB = defaultMaxUploadMB
	}
	if c.Rules == nil {
		c.Rules = []Rule{}
	}
}

func validateConfig(c *Config) error {
	applyDefaults(c)
	if len(c.SiteName) > 100 {
		return errors.New("site_name 过长")
	}
	if c.ListenPort < 1 || c.ListenPort > 65535 {
		return errors.New("listen_port 必须在 1–65535 之间")
	}
	if strings.ContainsAny(c.ListenHost, " \t\r\n/") {
		return errors.New("listen_host 不能包含空白或 /")
	}
	if strings.Contains(c.ListenHost, ":") && net.ParseIP(c.ListenHost) == nil {
		return errors.New("listen_host 只填 IP 或主机名，端口请写到 listen_port")
	}
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("log_level 必须是 debug/info/warn/error 之一")
	}
	if c.SessionHours < 1 || c.SessionHours > 8760 {
		return errors.New("session_hours 必须在 1–8760 之间")
	}
	if c.MaxUploadMB < 1 || c.MaxUploadMB > 1<<20 {
		return errors.New("max_upload_mb 必须在 1–1048576 之间")
	}
	if len(c.Rules) > maxRules {
		return fmt.Errorf("规则数量超过上限 %d", maxRules)
	}
	rules := make([]Rule, 0, len(c.Rules))
	for i := range c.Rules {
		r := c.Rules[i]
		// The public mode no longer exists: every rule is a lock and locks its
		// whole subtree. Dropping old public exceptions keeps older configs
		// loadable and is the safe direction (paths stay locked).
		if r.Mode == modeLegacyPublic {
			continue
		}
		if r.Mode == "" {
			r.Mode = modeLock
		}
		if r.Path == "" {
			return fmt.Errorf("规则 #%d: 路径为空", i+1)
		}
		// Older configs used a trailing /** to mean "this directory and below".
		// That is now the default behaviour of a directory lock.
		if strings.HasSuffix(r.Path, "/**") {
			r.Path = strings.TrimSuffix(r.Path, "/**")
			if r.Path == "" {
				r.Path = "/"
			}
		}
		abs, err := normalizeAbsPath(r.Path)
		if err != nil {
			return fmt.Errorf("规则 #%d: %w", i+1, err)
		}
		r.Path = abs
		if !isExactPath(r.Path) {
			return fmt.Errorf("规则 #%d: 路径不能包含通配符", i+1)
		}
		if len(r.Password) > 512 {
			return fmt.Errorf("规则 #%d: 密码过长", i+1)
		}
		rules = append(rules, r)
	}
	c.Rules = rules
	// Compile once to validate modes, passwords and overlapping paths.
	if _, err := compileRules(c.Rules, []byte("validation")); err != nil {
		return err
	}
	return nil
}

func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	const hexdigits = "0123456789abcdef"
	out := make([]byte, n*2)
	for i, v := range buf {
		out[i*2] = hexdigits[v>>4]
		out[i*2+1] = hexdigits[v&0x0f]
	}
	return string(out), nil
}

// loadConfig reads the config file. If it does not exist, it creates one with
// the default values (admin_password is empty, i.e. login needs no password).
func loadConfig(path string) (cfg *Config, created bool, err error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		cfg = &Config{}
		applyDefaults(cfg)
		if err := saveConfig(path, cfg); err != nil {
			return nil, false, err
		}
		return cfg, true, nil
	}
	if err != nil {
		return nil, false, err
	}
	cfg = &Config{}
	if err := decodeConfig(data, cfg); err != nil {
		return nil, false, fmt.Errorf("解析配置失败: %w", err)
	}
	if err := validateConfig(cfg); err != nil {
		return nil, false, fmt.Errorf("配置校验失败: %w", err)
	}
	return cfg, false, nil
}

// configWire mirrors Config with pointers so missing JSON keys can be detected.
// Keep it in sync with Config.
type configWire struct {
	SiteName       *string `json:"site_name"`
	ListenHost     *string `json:"listen_host"`
	ListenPort     *int    `json:"listen_port"`
	AdminPassword  *string `json:"admin_password"`
	SessionHours   *int    `json:"session_hours"`
	MaxUploadMB    *int64  `json:"max_upload_mb"`
	ShowHidden     *bool   `json:"show_hidden"`
	LogLevel       *string `json:"log_level"`
	TrustProxy     *bool   `json:"trust_proxy"`
	InsecureCookie *bool   `json:"insecure_cookie"`
	Rules          *[]Rule `json:"rules"`
}

// decodeConfig parses the JSON config strictly: unknown fields and missing
// fields are both rejected.
func decodeConfig(data []byte, cfg *Config) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var w configWire
	if err := dec.Decode(&w); err != nil {
		return err
	}
	if err := ensureEOF(dec); err != nil {
		return err
	}

	var missing []string
	add := func(name string, ok bool) {
		if !ok {
			missing = append(missing, name)
		}
	}
	add("site_name", w.SiteName != nil)
	add("listen_host", w.ListenHost != nil)
	add("listen_port", w.ListenPort != nil)
	add("admin_password", w.AdminPassword != nil)
	add("session_hours", w.SessionHours != nil)
	add("max_upload_mb", w.MaxUploadMB != nil)
	add("show_hidden", w.ShowHidden != nil)
	add("log_level", w.LogLevel != nil)
	add("trust_proxy", w.TrustProxy != nil)
	add("insecure_cookie", w.InsecureCookie != nil)
	add("rules", w.Rules != nil)
	if len(missing) > 0 {
		return fmt.Errorf("配置缺少字段: %s", strings.Join(missing, ", "))
	}

	cfg.SiteName = *w.SiteName
	cfg.ListenHost = *w.ListenHost
	cfg.ListenPort = *w.ListenPort
	cfg.AdminPassword = *w.AdminPassword
	cfg.SessionHours = *w.SessionHours
	cfg.MaxUploadMB = *w.MaxUploadMB
	cfg.ShowHidden = *w.ShowHidden
	cfg.LogLevel = *w.LogLevel
	cfg.TrustProxy = *w.TrustProxy
	cfg.InsecureCookie = *w.InsecureCookie
	cfg.Rules = *w.Rules
	if cfg.Rules == nil {
		cfg.Rules = []Rule{}
	}
	return nil
}

func ensureEOF(dec *json.Decoder) error {
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return errors.New("配置文件中存在多余内容")
	}
	return nil
}

// marshalConfig renders the config as indented JSON.
func marshalConfig(c *Config) ([]byte, error) {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// saveConfig writes atomically, keeping one .bak of the previous content.
func saveConfig(path string, c *Config) error {
	if err := validateConfig(c); err != nil {
		return err
	}
	data, err := marshalConfig(c)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if old, err := os.ReadFile(path); err == nil {
		if err := os.WriteFile(path+".bak", old, 0o600); err != nil {
			slog.Warn("备份配置失败", "err", err)
		}
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Chmod(path, 0o600)
}

// ensureSecret reads or creates the session signing key.
func ensureSecret(path string) ([]byte, error) {
	if data, err := os.ReadFile(path); err == nil {
		if len(data) != 32 {
			return nil, fmt.Errorf("%s 长度异常（%d 字节），期望 32 字节", path, len(data))
		}
		return data, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, secret, 0o600); err != nil {
		return nil, err
	}
	return secret, nil
}

// configManager owns the in-memory config and compiled rules, and reloads on
// external edits (checked at most once per second).
type configManager struct {
	mu        sync.Mutex
	path      string
	secret    []byte
	cfg       *Config
	rules     []compiledRule
	mtime     time.Time
	size      int64
	lastCheck time.Time
}

func newConfigManager(path string, secret []byte) *configManager {
	return &configManager{path: path, secret: secret}
}

func (cm *configManager) Load() (bool, error) {
	cfg, created, err := loadConfig(cm.path)
	if err != nil {
		return false, err
	}
	rules, err := compileRules(cfg.Rules, cm.secret)
	if err != nil {
		return false, err
	}
	st, _ := os.Stat(cm.path)
	cm.mu.Lock()
	cm.cfg = cfg
	cm.rules = rules
	if st != nil {
		cm.mtime = st.ModTime()
		cm.size = st.Size()
	}
	cm.lastCheck = time.Now()
	cm.mu.Unlock()
	return created, nil
}

func (cm *configManager) reloadLocked() {
	st, err := os.Stat(cm.path)
	if err != nil {
		return
	}
	if st.ModTime().Equal(cm.mtime) && st.Size() == cm.size {
		return
	}
	cfg, _, err := loadConfig(cm.path)
	if err != nil {
		slog.Error("配置文件重新加载失败，继续使用旧配置", "err", err)
		cm.mtime = st.ModTime()
		cm.size = st.Size()
		return
	}
	rules, err := compileRules(cfg.Rules, cm.secret)
	if err != nil {
		slog.Error("规则编译失败，继续使用旧配置", "err", err)
		return
	}
	if cm.cfg != nil {
		if cm.cfg.ListenHost != cfg.ListenHost || cm.cfg.ListenPort != cfg.ListenPort {
			slog.Warn("listen_host / listen_port 已修改，需要重启后生效",
				"running", cm.cfg.ListenAddr(), "configured", cfg.ListenAddr())
		}
	}
	cm.cfg = cfg
	cm.rules = rules
	cm.mtime = st.ModTime()
	cm.size = st.Size()
	appLogLevel.Set(parseLogLevel(cfg.LogLevel))
	slog.Info("配置已重新加载", "rules", len(rules))
}

// Current returns a snapshot of the config and compiled rules.
func (cm *configManager) Current() (*Config, []compiledRule) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	if time.Since(cm.lastCheck) > time.Second {
		cm.lastCheck = time.Now()
		cm.reloadLocked()
	}
	return cm.cfg.Clone(), append([]compiledRule(nil), cm.rules...)
}

// Update applies fn to a copy of the config, validates and persists it.
func (cm *configManager) Update(fn func(*Config) error) (*Config, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	next := cm.cfg.Clone()
	if err := fn(next); err != nil {
		return nil, err
	}
	if err := validateConfig(next); err != nil {
		return nil, err
	}
	rules, err := compileRules(next.Rules, cm.secret)
	if err != nil {
		return nil, err
	}
	if err := saveConfig(cm.path, next); err != nil {
		return nil, err
	}
	cm.cfg = next
	cm.rules = rules
	appLogLevel.Set(parseLogLevel(next.LogLevel))
	if st, err := os.Stat(cm.path); err == nil {
		cm.mtime = st.ModTime()
		cm.size = st.Size()
	}
	return next.Clone(), nil
}

// SetSecret rotates the signing key and recompiles rule IDs.
func (cm *configManager) SetSecret(secret []byte) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	rules, err := compileRules(cm.cfg.Rules, secret)
	if err != nil {
		return err
	}
	cm.secret = secret
	cm.rules = rules
	return nil
}
