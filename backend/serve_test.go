package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func freeAddr(t *testing.T) string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

func selfSigned(t *testing.T, dir string) (certFile, keyFile string) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "repoman.test"}, DNSNames: []string{"repoman.test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	kb, _ := x509.MarshalECPrivateKey(key)
	certFile, keyFile = filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600)
	os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0o600)
	return
}

// The real server loop: starts (plain and TLS), answers, writes its log file, and stops cleanly when asked.
func TestServeLifecycle(t *testing.T) {
	for _, withTLS := range []bool{false, true} {
		dir := t.TempDir()
		cfg := Config{Addr: freeAddr(t), DataDir: filepath.Join(dir, "data"), AdminPassword: "first-run-pass", TrustedProxies: loopbackProxies,
			AccessLog: true, LogFile: filepath.Join(dir, "logs", "repoman.log")}
		scheme := "http"
		if withTLS {
			cfg.TLSCert, cfg.TLSKey = selfSigned(t, dir)
			scheme = "https"
		}
		ctx, stop := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- serve(ctx, cfg) }()

		client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
		var resp *http.Response
		var err error
		for i := 0; i < 100; i++ {
			if resp, err = client.Get(scheme + "://" + cfg.Addr + "/healthz"); err == nil {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if err != nil {
			t.Fatalf("tls=%v: server did not come up: %v", withTLS, err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("tls=%v: healthz = %d", withTLS, resp.StatusCode)
		}
		if got := resp.Header.Get("Strict-Transport-Security"); (got != "") != withTLS {
			t.Fatalf("tls=%v: HSTS header = %q", withTLS, got)
		}
		req, _ := http.NewRequest("POST", scheme+"://"+cfg.Addr+"/api/auth/login", strings.NewReader(`{"username":"admin","password":"first-run-pass"}`))
		resp, err = client.Do(req)
		if err != nil || resp.StatusCode != 200 {
			t.Fatalf("tls=%v: login with RM_ADMIN_PASSWORD failed: %v %v", withTLS, err, resp)
		}
		resp.Body.Close()
		if secure := strings.Contains(resp.Header.Get("Set-Cookie"), "Secure"); secure != withTLS {
			t.Fatalf("tls=%v: session cookie Secure flag = %v", withTLS, secure)
		}

		stop()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("tls=%v: serve returned %v", withTLS, err)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("tls=%v: server did not shut down", withTLS)
		}
		if log, _ := os.ReadFile(cfg.LogFile); !strings.Contains(string(log), "listening on "+scheme+"://") || !strings.Contains(string(log), "stopped") {
			t.Fatalf("tls=%v: log file incomplete:\n%s", withTLS, log)
		}
		if _, err := os.Stat(filepath.Join(cfg.DataDir, "initial-admin-password")); !os.IsNotExist(err) {
			t.Fatal("a password supplied through RM_ADMIN_PASSWORD must not be written to disk")
		}
	}
}

func TestServeFailsFast(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	err := serve(context.Background(), Config{Addr: ln.Addr().String(), DataDir: t.TempDir(), AdminPassword: "first-run-pass"})
	if err == nil || !strings.Contains(err.Error(), "cannot listen") {
		t.Fatalf("a busy port should be reported clearly, got %v", err)
	}
	err = serve(context.Background(), Config{Addr: freeAddr(t), DataDir: t.TempDir(), AdminPassword: "short"})
	if err == nil || !strings.Contains(err.Error(), "RM_ADMIN_PASSWORD") {
		t.Fatalf("a too-short RM_ADMIN_PASSWORD should be reported clearly, got %v", err)
	}
}

func TestResetPasswordCommand(t *testing.T) {
	dir := t.TempDir()
	a, err := newApp(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.bootstrap("first-run-pass"); err != nil {
		t.Fatal(err)
	}
	a.db.Exec(`UPDATE users SET disabled=1 WHERE username='admin'`)
	a.db.Close()
	os.WriteFile(filepath.Join(dir, "none.env"), nil, 0o600)
	t.Setenv("RM_CONFIG", filepath.Join(dir, "none.env")) // never pick up a developer's local repoman.env

	// flags after the user name must be honoured, not silently dropped
	if err := cmdResetPassword([]string{"admin", "--password", "chosen-password-1", "--data-dir", dir}); err != nil {
		t.Fatal(err)
	}
	if err := cmdResetPassword([]string{"--data-dir", dir, "ghost"}); err == nil || !strings.Contains(err.Error(), "no user named") {
		t.Fatalf("unknown user: %v", err)
	}
	if err := cmdResetPassword([]string{"--data-dir", filepath.Join(dir, "missing")}); err == nil || !strings.Contains(err.Error(), "no RepoMan database") {
		t.Fatalf("a wrong data directory must be reported: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "missing")); !os.IsNotExist(err) {
		t.Fatal("reset-password created a data directory")
	}
	if err := cmdResetPassword([]string{"--data-dir", dir, "--password", "short"}); err == nil {
		t.Fatal("a too-short password must be refused")
	}

	a, _ = newApp(dir)
	defer a.db.Close()
	if a.verifyPassword("admin", "chosen-password-1") == nil {
		t.Fatal("the new password does not work (or the account is still disabled)")
	}
	if a.verifyPassword("admin", "first-run-pass") != nil {
		t.Fatal("the old password still works")
	}
	if a.getJSON("initial_password", new(string)) {
		t.Fatal("the first-run password reminder should be cleared by a reset")
	}
}
