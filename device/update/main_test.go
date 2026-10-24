package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSemver(t *testing.T) {
	a := mustVer(t, "0.3.0")
	b := mustVer(t, "0.3.1")
	if !a.less(b) || b.less(a) {
		t.Fatal("0.3.0 should be less than 0.3.1")
	}
	if mustVer(t, "v1.2.3-rc1").String() != "1.2.3" {
		t.Fatal("suffix should be ignored")
	}
	if _, err := parseSemver("1.2"); err == nil {
		t.Fatal("1.2 should fail")
	}
}

func mustVer(t *testing.T, s string) semver {
	t.Helper()
	v, err := parseSemver(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestCheckVerifiesSignature(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	pubPath := filepath.Join(dir, "release.pub")
	der, _ := x509.MarshalPKIXPublicKey(pub)
	if err := os.WriteFile(pubPath, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), 0o644); err != nil {
		t.Fatal(err)
	}
	versionPath := filepath.Join(dir, "VERSION")
	os.WriteFile(versionPath, []byte("0.3.0\n"), 0o644)

	name := "linux-" + runtime.GOARCH
	m := manifest{
		Schema: 1, Version: "0.3.1", Channel: "stable", MinVersion: "0.2.0",
		Assets: map[string]asset{name: {URL: "https://example.invalid/x.tar.gz", SHA256: strings.Repeat("a", 64), Size: 1}},
	}
	raw, _ := json.Marshal(m)
	sig := ed25519.Sign(priv, raw)

	mux := http.NewServeMux()
	mux.HandleFunc("/manifest.json", func(w http.ResponseWriter, r *http.Request) { w.Write(raw) })
	mux.HandleFunc("/manifest.json.sig", func(w http.ResponseWriter, r *http.Request) { w.Write(sig) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cfg := config{
		manifestURL: srv.URL + "/manifest.json",
		pubKey:      pubPath,
		versionFile: versionPath,
		allowHTTP:   true,
	}
	res, _, err := check(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || res.Latest != "0.3.1" || res.Current != "0.3.0" {
		t.Fatalf("check = %+v", res)
	}

	// Tamper with the manifest: same signature must be rejected.
	raw2 := append([]byte{}, raw...)
	raw2[len(raw2)-2] ^= 1
	mux.HandleFunc("/bad.json", func(w http.ResponseWriter, r *http.Request) { w.Write(raw2) })
	mux.HandleFunc("/bad.json.sig", func(w http.ResponseWriter, r *http.Request) { w.Write(sig) })
	cfg.manifestURL = srv.URL + "/bad.json"
	if _, _, err := check(cfg); err == nil || !strings.Contains(err.Error(), "签名") {
		t.Fatalf("tampered manifest accepted: %v", err)
	}
}

func TestLoadPublicKeysDirectory(t *testing.T) {
	dir := t.TempDir()
	for i := range 2 {
		pub, _, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		der, _ := x509.MarshalPKIXPublicKey(pub)
		path := filepath.Join(dir, string(rune('a'+i))+".pub")
		if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	keys, err := loadPublicKeys(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 {
		t.Fatalf("got %d keys", len(keys))
	}
}

func TestSafeTarName(t *testing.T) {
	for _, ok := range []string{
		"https://x/nokeyout-0.3.1-linux-arm64.tar.gz",
		"https://x/nokeyout-0.3.1.tar.gz?token=1",
	} {
		if _, err := safeTarName(ok); err != nil {
			t.Errorf("safeTarName(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{
		"https://x/evil.tar.gz",
		"https://x/nokeyout-0.3.1.tar.gz/../evil",
		"https://x/nokeyout-0.3.1.txt",
	} {
		if _, err := safeTarName(bad); err == nil {
			t.Errorf("safeTarName(%q) should fail", bad)
		}
	}
}

func TestValidateManifestRejectsBadAssets(t *testing.T) {
	m := manifest{Schema: 1, Version: "0.3.1", Assets: map[string]asset{
		"linux-arm64": {URL: "https://x/a.tar.gz", SHA256: "xyz"},
	}}
	if err := validateManifest(m); err == nil {
		t.Fatal("bad sha256 accepted")
	}
}
