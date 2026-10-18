package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

var (
	version = "0.3.1"
	commit  = "none"
	date    = "unknown"
)

// appLogLevel is updated when config.json changes, so log_level takes effect
// without a restart.
var appLogLevel = new(slog.LevelVar)

func parseLogLevel(s string) slog.Level {
	switch s {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// accessURL turns a bound address into a browsable URL, replacing wildcard
// hosts with loopback so the printed link is actually clickable.
func accessURL(addr net.Addr) string {
	host, port, err := net.SplitHostPort(addr.String())
	if err != nil {
		return "http://" + addr.String() + "/"
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
		host = "127.0.0.1"
	}
	if host == "" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port) + "/"
}

func usage() {
	out := flag.CommandLine.Output()
	fmt.Fprintf(out, "用法:\n  %s [选项] [目录]\n\n", os.Args[0])
	fmt.Fprintln(out, "目录缺省为启动时的当前目录；选项可以放在目录前后。")
	fmt.Fprintln(out)
	flag.PrintDefaults()
}

// reorderArgs moves flags in front of positional arguments so that a target
// directory may appear anywhere on the command line.
func reorderArgs(argv []string) []string {
	valueFlags := map[string]bool{"config": true}
	var flags, positionals []string
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		if a == "--" {
			positionals = append(positionals, argv[i+1:]...)
			out := append(flags, "--")
			return append(out, positionals...)
		}
		if len(a) > 1 && a[0] == '-' {
			flags = append(flags, a)
			name := strings.TrimLeft(a, "-")
			if !strings.Contains(name, "=") && valueFlags[name] && i+1 < len(argv) {
				i++
				flags = append(flags, argv[i])
			}
			continue
		}
		positionals = append(positionals, a)
	}
	return append(flags, positionals...)
}

// parseArgs interprets the single optional positional argument: "[目录]".
func parseArgs(args []string, cwd string) (rootDir string, err error) {
	switch len(args) {
	case 0:
		return cwd, nil
	case 1:
		return resolveDir(args[0], cwd)
	default:
		return "", errors.New("参数过多，用法: tinyhutch [选项] [目录]")
	}
}

func resolveDir(p, cwd string) (string, error) {
	if !filepath.IsAbs(p) {
		p = filepath.Join(cwd, p)
	}
	p = filepath.Clean(p)
	info, err := os.Stat(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("目录不存在: %s", p)
		}
		return "", fmt.Errorf("无法访问目录 %s: %w", p, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("不是目录: %s", p)
	}
	return p, nil
}

// resolveConfigPath returns --config when set, otherwise <目录>/config.json.
func resolveConfigPath(rootDir, override, cwd string) string {
	if override == "" {
		return filepath.Join(rootDir, "config.json")
	}
	if !filepath.IsAbs(override) {
		override = filepath.Join(cwd, override)
	}
	p, _ := filepath.Abs(override)
	return p
}

func main() {
	var (
		configFlag  = flag.String("config", "", "配置文件路径（默认 <目录>/config.json）")
		showVersion = flag.Bool("version", false, "打印版本后退出")
	)
	flag.Usage = usage
	flag.CommandLine.Parse(reorderArgs(os.Args[1:]))

	if *showVersion {
		fmt.Printf("tinyhutch %s (commit %s, built %s)\n", version, commit, date)
		return
	}

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: appLogLevel})))

	cwd, err := os.Getwd()
	if err != nil {
		slog.Error("无法获取当前目录", "err", err)
		os.Exit(1)
	}
	cwd, err = filepath.Abs(cwd)
	if err != nil {
		slog.Error("无法解析当前目录", "err", err)
		os.Exit(1)
	}

	rootDir, err := parseArgs(flag.Args(), cwd)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		flag.Usage()
		os.Exit(2)
	}

	configPath := resolveConfigPath(rootDir, *configFlag, cwd)

	secretPath := filepath.Join(rootDir, ".tinyhutch.secret")
	secret, err := ensureSecret(secretPath)
	if err != nil {
		slog.Error("读取或生成会话密钥失败", "err", err)
		os.Exit(1)
	}

	cm := newConfigManager(configPath, secret)
	created, err := cm.Load()
	if err != nil {
		slog.Error("加载配置失败", "err", err)
		os.Exit(1)
	}

	files, err := newFileSystem(rootDir, configPath, secretPath)
	if err != nil {
		slog.Error("打开根目录失败", "err", err)
		os.Exit(1)
	}
	defer files.Close()

	srv := &server{
		cm:         cm,
		files:      files,
		limiter:    newLimiter(),
		version:    version,
		rootDir:    rootDir,
		configPath: configPath,
		secretPath: secretPath,
		start:      time.Now(),
	}
	srv.secret.Store(secret)

	cfg, _ := cm.Current()
	appLogLevel.Set(parseLogLevel(cfg.LogLevel))

	httpSrv := &http.Server{
		Handler:           srv.routes(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	ln, err := net.Listen("tcp", cfg.ListenAddr())
	if err != nil {
		slog.Error("监听失败", "addr", cfg.ListenAddr(), "err", err)
		os.Exit(1)
	}

	fmt.Printf("TinyHutch %s 已启动  →  %s\n", version, accessURL(ln.Addr()))
	slog.Debug("启动参数", "version", version, "root", rootDir, "config", configPath, "listen", cfg.ListenAddr())

	if created {
		slog.Info("首次启动，已生成配置文件", "path", configPath)
	}
	if cfg.AdminPassword == "" {
		slog.Warn("admin_password 为空：任何点击登录的人都会获得管理员权限")
	}

	go func() {
		files.cleanupTrash()
		if n, err := files.cleanupOldUploads(24 * time.Hour); err != nil {
			slog.Warn("清理上传临时文件失败", "err", err)
		} else if n > 0 {
			slog.Info("已清理过期的上传临时文件", "count", n)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		errCh <- httpSrv.Serve(ln)
	}()

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("HTTP 服务退出", "err", err)
			os.Exit(1)
		}
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := httpSrv.Shutdown(shutdownCtx); err != nil {
			slog.Warn("优雅退出超时", "err", err)
		}
		slog.Info("已停止")
	}
}
