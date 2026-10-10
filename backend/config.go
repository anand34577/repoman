package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
)

// Config is the process-level configuration. Everything else (repositories, users, SSO,
// notifications…) lives in the database and is managed from the UI or the REST API.
//
// Precedence: command-line flag > environment variable > env file > default.
type Config struct {
	Addr           string // RM_ADDR: listen address
	DataDir        string // RM_DATA_DIR: database, blobs, signing key
	AdminPassword  string // RM_ADMIN_PASSWORD: first-run admin password (random if empty)
	TLSCert        string // RM_TLS_CERT + RM_TLS_KEY: serve HTTPS directly
	TLSKey         string
	LogFile        string         // RM_LOG_FILE: also write logs to this (rotated) file
	AccessLog      bool           // RM_ACCESS_LOG: log every HTTP request
	TrustedProxies []netip.Prefix // RM_TRUSTED_PROXIES: peers whose X-Forwarded-* headers are believed
}

var loopbackProxies = []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("::1/128")}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// exeDir is the directory holding the binary. A Windows service starts in System32, so
// relative paths and the default env file are resolved against this directory instead.
func exeDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	return filepath.Dir(exe)
}

// loadEnvFile reads KEY=VALUE lines into the environment. Variables that are already set win,
// so a real environment variable always overrides the file.
func loadEnvFile(file string) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(strings.TrimPrefix(sc.Text(), string([]byte{0xEF, 0xBB, 0xBF}))) // Notepad writes a BOM
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(strings.TrimPrefix(line, "export "), "=")
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if !ok || k == "" || strings.ContainsAny(k, " \t") {
			return fmt.Errorf("%s:%d: expected KEY=VALUE", file, n)
		}
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		}
		if _, set := os.LookupEnv(k); !set {
			os.Setenv(k, v)
		}
	}
	return sc.Err()
}

func parseProxies(s string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == ';' }) {
		if !strings.Contains(f, "/") { // a single address
			a, err := netip.ParseAddr(f)
			if err != nil {
				return nil, fmt.Errorf("RM_TRUSTED_PROXIES: %q is not an IP address or CIDR range", f)
			}
			out = append(out, netip.PrefixFrom(a, a.BitLen()))
			continue
		}
		p, err := netip.ParsePrefix(f)
		if err != nil {
			return nil, fmt.Errorf("RM_TRUSTED_PROXIES: %q is not an IP address or CIDR range", f)
		}
		out = append(out, p.Masked())
	}
	return out, nil
}

// loadConfig resolves the configuration for a command. Extra flags can be registered by the caller.
func loadConfig(name string, args []string, extra func(*flag.FlagSet)) (Config, []string, error) {
	fs := flag.NewFlagSet("repoman "+name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	cfgFile := fs.String("config", "", "env file with RM_* settings (default: repoman.env next to the binary or in the working directory)")
	addr := fs.String("addr", "", "listen address, e.g. :8081 or 127.0.0.1:8081 (RM_ADDR)")
	dataDir := fs.String("data-dir", "", "directory for the database and artifacts (RM_DATA_DIR)")
	tlsCert := fs.String("tls-cert", "", "TLS certificate chain, PEM (RM_TLS_CERT)")
	tlsKey := fs.String("tls-key", "", "TLS private key, PEM (RM_TLS_KEY)")
	if extra != nil {
		extra(fs)
	}
	// flags may come before or after positional arguments: "reset-password alice --password x"
	var positional []string
	for rest := args; ; {
		if err := fs.Parse(rest); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				fs.SetOutput(os.Stdout)
				fmt.Printf("Usage: repoman %s [flags]\n\n", name)
				fs.PrintDefaults()
				os.Exit(0)
			}
			return Config{}, nil, err
		}
		if rest = fs.Args(); len(rest) == 0 {
			break
		}
		positional, rest = append(positional, rest[0]), rest[1:]
	}

	file := *cfgFile
	if file == "" {
		file = os.Getenv("RM_CONFIG")
	}
	if file != "" {
		if err := loadEnvFile(file); err != nil {
			return Config{}, nil, fmt.Errorf("config file: %w", err)
		}
	} else {
		for _, cand := range []string{filepath.Join(exeDir(), "repoman.env"), "repoman.env"} {
			if _, err := os.Stat(cand); err == nil {
				if err := loadEnvFile(cand); err != nil {
					return Config{}, nil, fmt.Errorf("config file: %w", err)
				}
				break
			}
		}
	}

	pick := func(flagVal, key, def string) string {
		if flagVal != "" {
			return flagVal
		}
		return env(key, def)
	}
	c := Config{
		Addr:          pick(*addr, "RM_ADDR", ":8081"),
		DataDir:       pick(*dataDir, "RM_DATA_DIR", "data"),
		AdminPassword: os.Getenv("RM_ADMIN_PASSWORD"),
		TLSCert:       pick(*tlsCert, "RM_TLS_CERT", ""),
		TLSKey:        pick(*tlsKey, "RM_TLS_KEY", ""),
		LogFile:       os.Getenv("RM_LOG_FILE"),
		AccessLog:     !isFalse(os.Getenv("RM_ACCESS_LOG")),
	}
	if (c.TLSCert == "") != (c.TLSKey == "") {
		return c, nil, errors.New("RM_TLS_CERT and RM_TLS_KEY must be set together")
	}
	c.TrustedProxies = loopbackProxies
	if v, set := os.LookupEnv("RM_TRUSTED_PROXIES"); set {
		p, err := parseProxies(v)
		if err != nil {
			return c, nil, err
		}
		c.TrustedProxies = p // an empty value trusts nobody
	}
	if runningAsService() { // the working directory of a service is not ours
		if !filepath.IsAbs(c.DataDir) {
			c.DataDir = filepath.Join(exeDir(), c.DataDir)
		}
		if c.LogFile == "" {
			c.LogFile = filepath.Join(c.DataDir, "logs", "repoman.log")
		}
	}
	return c, positional, nil
}

func isFalse(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "0", "false", "no", "off":
		return true
	}
	return false
}
