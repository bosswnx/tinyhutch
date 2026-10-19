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
	fmt.Fprintf(out, "Usage:\n  %s [options] [directory]\n\n", os.Args[0])
	fmt.Fprintln(out, "The directory defaults to the current working directory; options may go before or after it.")
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

// parseArgs interprets the single optional positional argument: "[directory]".
func parseArgs(args []string, cwd string) (rootDir string, err error) {
	switch len(args) {
	case 0:
		return cwd, nil
	case 1:
		return resolveDir(args[0], cwd)
	default:
		return "", errors.New("too many arguments; usage: tinyhutch [options] [directory]")
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
			return "", fmt.Errorf("directory not found: %s", p)
		}
		return "", fmt.Errorf("cannot access directory %s: %w", p, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("not a directory: %s", p)
	}
	return p, nil
}

// resolveConfigPath returns --config when set, otherwise <directory>/config.json.
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
		configFlag  = flag.String("config", "", "path to the config file (default <directory>/config.json)")
		showVersion = flag.Bool("version", false, "print the version and exit")
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
		slog.Error("cannot get the current directory", "err", err)
		os.Exit(1)
	}
	cwd, err = filepath.Abs(cwd)
	if err != nil {
		slog.Error("cannot resolve the current directory", "err", err)
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
		slog.Error("cannot read or create the session secret", "err", err)
		os.Exit(1)
	}

	cm := newConfigManager(configPath, secret)
	created, err := cm.Load()
	if err != nil {
		slog.Error("cannot load the config", "err", err)
		os.Exit(1)
	}

	files, err := newFileSystem(rootDir, configPath, secretPath)
	if err != nil {
		slog.Error("cannot open the root directory", "err", err)
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
		slog.Error("cannot listen", "addr", cfg.ListenAddr(), "err", err)
		os.Exit(1)
	}

	fmt.Printf("TinyHutch %s is running  →  %s\n", version, accessURL(ln.Addr()))
	slog.Debug("startup", "version", version, "root", rootDir, "config", configPath, "listen", cfg.ListenAddr())

	if created {
		slog.Info("first start: created the config file", "path", configPath)
	}
	if cfg.AdminPassword == "" {
		slog.Warn("admin_password is empty: anyone who clicks Login becomes an admin")
	}

	go func() {
		files.cleanupTrash()
		if n, err := files.cleanupOldUploads(24 * time.Hour); err != nil {
			slog.Warn("failed to clean up temporary upload files", "err", err)
		} else if n > 0 {
			slog.Info("cleaned up stale temporary upload files", "count", n)
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
			slog.Error("HTTP server exited", "err", err)
			os.Exit(1)
		}
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := httpSrv.Shutdown(shutdownCtx); err != nil {
			slog.Warn("graceful shutdown timed out", "err", err)
		}
		slog.Info("stopped")
	}
}
