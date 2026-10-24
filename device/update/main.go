// nokeyout-update — signed release client for NoKeyOut.
//
// It never installs code by itself: nk-deploy does the backup/switch/smoke/
// rollback. This tool verifies a signed manifest, stages the matching asset,
// and starts nk-deploy in its own systemd cgroup so a panel restart cannot
// kill the updater mid-flight.
package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	defaultManifest  = "https://nokeyout.com/releases/stable/manifest.json"
	defaultPubKey    = "/etc/nokeyout/release.pub"
	defaultVersion   = "/var/lib/nokeyout/VERSION"
	defaultBackup    = "/var/lib/nokeyout/last-good-nk"
	defaultMagpie    = "/var/lib/nokeyout/last-good-version"
	defaultUpdateDir = "/var/lib/nokeyout/update"
	defaultUpgrade   = "/usr/local/bin/nokeyout-upgrade"
	manifestLimit    = 1 << 20
	sigLimit         = 4096
	assetLimit       = 512 << 20
)

type asset struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type manifest struct {
	Schema     int              `json:"schema"`
	Version    string           `json:"version"`
	Channel    string           `json:"channel"`
	MinVersion string           `json:"minVersion"`
	Magpie     string           `json:"magpie"`
	Notes      string           `json:"notes"`
	Assets     map[string]asset `json:"assets"`
}

type config struct {
	manifestURL string
	pubKey      string
	versionFile string
	backupFile  string
	magpieFile  string
	updateDir   string
	stateFile   string
	upgrade     string
	allowHTTP   bool
}

type updateState struct {
	State    string `json:"state"`
	Version  string `json:"version,omitempty"`
	Unit     string `json:"unit,omitempty"`
	Message  string `json:"message,omitempty"`
	Error    string `json:"error,omitempty"`
	Started  string `json:"started,omitempty"`
	Finished string `json:"finished,omitempty"`
}

func env(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func loadConfig() config {
	return config{
		manifestURL: env("NOKEYOUT_RELEASE_URL", defaultManifest),
		pubKey:      env("NOKEYOUT_RELEASE_PUBKEY", defaultPubKey),
		versionFile: env("NOKEYOUT_VERSION_FILE", defaultVersion),
		backupFile:  env("NOKEYOUT_BACKUP_FILE", defaultBackup),
		magpieFile:  env("NOKEYOUT_MAGPIE_VERSION_FILE", defaultMagpie),
		updateDir:   env("NOKEYOUT_UPDATE_DIR", defaultUpdateDir),
		stateFile:   env("NOKEYOUT_UPDATE_STATE", defaultUpdateDir+"/state.json"),
		upgrade:     env("NOKEYOUT_UPGRADE_BIN", defaultUpgrade),
		allowHTTP:   os.Getenv("NOKEYOUT_ALLOW_HTTP") == "1",
	}
}

type semver struct{ major, minor, patch int }

func parseSemver(s string) (semver, error) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return semver{}, fmt.Errorf("版本号格式不是 x.y.z: %q", s)
	}
	var v semver
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return semver{}, fmt.Errorf("版本号格式不是 x.y.z: %q", s)
		}
		switch i {
		case 0:
			v.major = n
		case 1:
			v.minor = n
		case 2:
			v.patch = n
		}
	}
	return v, nil
}

func (v semver) less(o semver) bool {
	if v.major != o.major {
		return v.major < o.major
	}
	if v.minor != o.minor {
		return v.minor < o.minor
	}
	return v.patch < o.patch
}

func (v semver) String() string { return fmt.Sprintf("%d.%d.%d", v.major, v.minor, v.patch) }

func readTrim(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func checkURL(raw string, allowHTTP bool) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("URL 非法: %q", raw)
	}
	if u.Scheme != "https" && !(allowHTTP && u.Scheme == "http") {
		return nil, fmt.Errorf("只允许 https（测试可设 NOKEYOUT_ALLOW_HTTP=1）: %s", raw)
	}
	return u, nil
}

func newHTTPClient(cfg config, timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("重定向过多")
			}
			_, err := checkURL(req.URL.String(), cfg.allowHTTP)
			return err
		},
	}
}

func httpGet(cfg config, raw string, limit int64) ([]byte, error) {
	if _, err := checkURL(raw, cfg.allowHTTP); err != nil {
		return nil, err
	}
	client := newHTTPClient(cfg, 30*time.Second)
	resp, err := client.Get(raw)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: HTTP %d", raw, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("响应超过 %d 字节", limit)
	}
	return b, nil
}

// loadPublicKeys accepts either one PEM file or a directory of *.pub files.
// A device can therefore trust the official key plus a fork/self-built key
// without patching the updater.
func loadPublicKeys(path string) ([]ed25519.PublicKey, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("读发布公钥失败: %w", err)
	}
	files := []string{path}
	if st.IsDir() {
		files, err = filepath.Glob(filepath.Join(path, "*.pub"))
		if err != nil || len(files) == 0 {
			return nil, fmt.Errorf("目录 %s 没有 *.pub", path)
		}
	}
	var keys []ed25519.PublicKey
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, fmt.Errorf("读发布公钥 %s: %w", f, err)
		}
		block, _ := pem.Decode(b)
		if block == nil {
			return nil, fmt.Errorf("发布公钥 %s 不是 PEM", f)
		}
		key, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("发布公钥 %s 解析失败: %w", f, err)
		}
		pub, ok := key.(ed25519.PublicKey)
		if !ok {
			return nil, fmt.Errorf("发布公钥 %s 不是 Ed25519", f)
		}
		keys = append(keys, pub)
	}
	return keys, nil
}

func fetchManifest(cfg config) (manifest, []byte, error) {
	raw, err := httpGet(cfg, cfg.manifestURL, manifestLimit)
	if err != nil {
		return manifest{}, nil, err
	}
	sigURL, err := url.Parse(cfg.manifestURL)
	if err != nil {
		return manifest{}, nil, err
	}
	sigURL.Path += ".sig"
	sig, err := httpGet(cfg, sigURL.String(), sigLimit)
	if err != nil {
		return manifest{}, nil, fmt.Errorf("下载签名失败: %w", err)
	}
	keys, err := loadPublicKeys(cfg.pubKey)
	if err != nil {
		return manifest{}, nil, err
	}
	verified := false
	if len(sig) == ed25519.SignatureSize {
		for _, pub := range keys {
			if ed25519.Verify(pub, raw, sig) {
				verified = true
				break
			}
		}
	}
	if !verified {
		return manifest{}, nil, errors.New("manifest 签名校验失败")
	}
	var m manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return manifest{}, nil, fmt.Errorf("manifest JSON 解析失败: %w", err)
	}
	if err := validateManifest(m); err != nil {
		return manifest{}, nil, err
	}
	return m, raw, nil
}

func validateManifest(m manifest) error {
	if m.Schema != 1 {
		return fmt.Errorf("不支持的 manifest schema: %d", m.Schema)
	}
	if _, err := parseSemver(m.Version); err != nil {
		return err
	}
	if m.MinVersion != "" {
		if _, err := parseSemver(m.MinVersion); err != nil {
			return fmt.Errorf("minVersion: %w", err)
		}
	}
	if len(m.Assets) == 0 {
		return errors.New("manifest 没有 assets")
	}
	for name, a := range m.Assets {
		if _, err := url.Parse(a.URL); err != nil || a.URL == "" {
			return fmt.Errorf("asset %s URL 非法", name)
		}
		if len(a.SHA256) != 64 {
			return fmt.Errorf("asset %s sha256 长度不对", name)
		}
		if _, err := hex.DecodeString(a.SHA256); err != nil {
			return fmt.Errorf("asset %s sha256 非法", name)
		}
		if a.Size < 0 {
			return fmt.Errorf("asset %s size 非法", name)
		}
	}
	return nil
}

func currentVersion(cfg config) (semver, error) {
	s := readTrim(cfg.versionFile)
	if s == "" {
		return semver{}, fmt.Errorf("读不到当前版本 %s", cfg.versionFile)
	}
	return parseSemver(s)
}

func pickAsset(m manifest, goarch string) (string, asset, error) {
	name := "linux-" + goarch
	a, ok := m.Assets[name]
	if !ok {
		return "", asset{}, fmt.Errorf("manifest 没有 %s 的发布物", name)
	}
	return name, a, nil
}

type checkResult struct {
	OK      bool   `json:"ok"`
	Current string `json:"current"`
	Latest  string `json:"latest"`
	Channel string `json:"channel"`
	Notes   string `json:"notes,omitempty"`
	Magpie  string `json:"magpie,omitempty"`
	Asset   string `json:"asset,omitempty"`
	Error   string `json:"error,omitempty"`
}

func check(cfg config) (checkResult, manifest, error) {
	m, _, err := fetchManifest(cfg)
	if err != nil {
		return checkResult{OK: false, Error: err.Error()}, manifest{}, err
	}
	cur, err := currentVersion(cfg)
	if err != nil {
		return checkResult{OK: false, Latest: m.Version, Error: err.Error()}, m, err
	}
	latest, _ := parseSemver(m.Version)
	if m.MinVersion != "" {
		min, _ := parseSemver(m.MinVersion)
		if cur.less(min) {
			err := fmt.Errorf("当前 %s 低于最低可升级版本 %s", cur, min)
			return checkResult{OK: false, Current: cur.String(), Latest: m.Version, Error: err.Error()}, m, err
		}
	}
	name, _, err := pickAsset(m, runtime.GOARCH)
	if err != nil {
		return checkResult{OK: false, Current: cur.String(), Latest: m.Version, Error: err.Error()}, m, err
	}
	res := checkResult{OK: true, Current: cur.String(), Latest: m.Version, Channel: m.Channel, Notes: m.Notes, Magpie: m.Magpie, Asset: name}
	if !cur.less(latest) {
		res.Notes = "已是最新版本"
	}
	return res, m, nil
}

func printJSON(v any) {
	b, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(b))
}

func cmdCheck(cfg config) int {
	res, _, err := check(cfg)
	printJSON(res)
	if err != nil {
		fmt.Fprintln(os.Stderr, "check:", err)
		return 1
	}
	return 0
}

func writeState(cfg config, st updateState) error {
	if err := os.MkdirAll(filepath.Dir(cfg.stateFile), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp := cfg.stateFile + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, cfg.stateFile)
}

func readState(cfg config) updateState {
	var st updateState
	if b, err := os.ReadFile(cfg.stateFile); err == nil {
		_ = json.Unmarshal(b, &st)
	}
	return st
}

func cmdStatus(cfg config) int {
	printJSON(map[string]any{
		"current":          readTrim(cfg.versionFile),
		"last_backup":      readTrim(cfg.backupFile),
		"magpie_last_good": readTrim(cfg.magpieFile),
		"state":            readState(cfg),
	})
	return 0
}

func isRoot() bool { return os.Geteuid() == 0 }

func safeTarName(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	name := filepath.Base(u.Path)
	if !strings.HasPrefix(name, "nokeyout-") || !strings.HasSuffix(name, ".tar.gz") {
		return "", fmt.Errorf("发布物文件名不合法: %q", name)
	}
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '-' || r == '_' {
			continue
		}
		return "", fmt.Errorf("发布物文件名含非法字符: %q", name)
	}
	return name, nil
}

func downloadAsset(cfg config, a asset, rawURL, version string) (string, error) {
	if _, err := checkURL(rawURL, cfg.allowHTTP); err != nil {
		return "", err
	}
	name, err := safeTarName(rawURL)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(cfg.updateDir, 0o755); err != nil {
		return "", err
	}
	dst := filepath.Join(cfg.updateDir, name)
	tmp := dst + ".tmp"
	client := newHTTPClient(cfg, 30*time.Minute)
	resp, err := client.Get(rawURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("下载发布物: HTTP %d", resp.StatusCode)
	}
	f, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, assetLimit+1))
	closeErr := f.Close()
	if copyErr != nil {
		os.Remove(tmp)
		return "", copyErr
	}
	if closeErr != nil {
		os.Remove(tmp)
		return "", closeErr
	}
	if n > assetLimit {
		os.Remove(tmp)
		return "", fmt.Errorf("发布物超过 %d 字节", assetLimit)
	}
	if a.Size > 0 && n != a.Size {
		os.Remove(tmp)
		return "", fmt.Errorf("发布物大小不符: got %d want %d", n, a.Size)
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, a.SHA256) {
		os.Remove(tmp)
		return "", fmt.Errorf("发布物 sha256 不符: got %s want %s", got, a.SHA256)
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		os.Remove(tmp)
		return "", err
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return "", err
	}
	return dst, nil
}

func newUnit() string {
	return fmt.Sprintf("nokeyout-update-%s-%d", time.Now().Format("20060102150405"), os.Getpid())
}

func startUnit(cfg config, unit, command string, args ...string) error {
	cmdArgs := []string{
		"--unit=" + unit,
		"--collect",
		"--property=Type=oneshot",
		"--property=TimeoutStartSec=1800",
		"--description=NoKeyOut self-update",
		"--setenv=NOKEYOUT_SCREEN=" + os.Getenv("NOKEYOUT_SCREEN"),
		"--setenv=NOKEYOUT_UPDATE_DIR=" + cfg.updateDir,
		"--setenv=NOKEYOUT_UPDATE_STATE=" + cfg.stateFile,
		"--setenv=NOKEYOUT_UPGRADE_BIN=" + cfg.upgrade,
		"--setenv=NOKEYOUT_VERSION_FILE=" + cfg.versionFile,
		command,
	}
	cmdArgs = append(cmdArgs, args...)
	out, err := exec.Command("systemd-run", cmdArgs...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemd-run 失败: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

func selfPath() string {
	p, err := os.Executable()
	if err != nil {
		return "/usr/local/bin/nokeyout-update"
	}
	return p
}

func cmdRunDeploy(cfg config, args []string) int {
	if !isRoot() {
		fmt.Fprintln(os.Stderr, "run-deploy 需要 root")
		return 1
	}
	if len(args) != 3 {
		fmt.Fprintln(os.Stderr, "内部命令: run-deploy <tar> <version> <unit>")
		return 2
	}
	tarPath, version, unit := args[0], args[1], args[2]
	writeState(cfg, updateState{State: "running", Version: version, Unit: unit,
		Started: time.Now().Format(time.RFC3339), Message: "nk-deploy running"})
	cmd := exec.Command(cfg.upgrade, "nk-deploy", tarPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	st := updateState{State: "success", Version: version, Unit: unit,
		Started: time.Now().Format(time.RFC3339), Finished: time.Now().Format(time.RFC3339),
		Message: "deployed"}
	if err != nil {
		st.State = "failed"
		st.Error = err.Error()
	}
	writeState(cfg, st)
	return exitCode(err)
}

func cmdRunRollback(cfg config, args []string) int {
	if !isRoot() {
		fmt.Fprintln(os.Stderr, "run-rollback 需要 root")
		return 1
	}
	unit := ""
	if len(args) > 0 {
		unit = args[0]
	}
	writeState(cfg, updateState{State: "rolling_back", Unit: unit,
		Started: time.Now().Format(time.RFC3339), Message: "nk-rollback running"})
	cmd := exec.Command(cfg.upgrade, "nk-rollback")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	st := updateState{State: "rolled_back", Unit: unit,
		Started: time.Now().Format(time.RFC3339), Finished: time.Now().Format(time.RFC3339),
		Message: "rolled back"}
	if err != nil {
		st.State = "failed"
		st.Error = err.Error()
	}
	writeState(cfg, st)
	return exitCode(err)
}

func cmdInstall(cfg config, args []string, foreground bool) int {
	if !isRoot() {
		fmt.Fprintln(os.Stderr, "install 需要 root")
		return 1
	}
	res, m, err := check(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "check:", err)
		return 1
	}
	cur, _ := parseSemver(res.Current)
	latest, _ := parseSemver(m.Version)
	if !cur.less(latest) {
		fmt.Fprintf(os.Stderr, "已是最新版本 %s\n", cur)
		return 0
	}
	if len(args) > 0 && args[0] != "" && args[0] != m.Version {
		fmt.Fprintf(os.Stderr, "manifest 最新版本是 %s，不是 %s\n", m.Version, args[0])
		return 1
	}
	name, a, err := pickAsset(m, runtime.GOARCH)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Fprintf(os.Stderr, "下载 %s %s\n", name, m.Version)
	tarPath, err := downloadAsset(cfg, a, a.URL, m.Version)
	if err != nil {
		fmt.Fprintln(os.Stderr, "下载失败:", err)
		return 1
	}
	if foreground {
		writeState(cfg, updateState{State: "running", Version: m.Version,
			Started: time.Now().Format(time.RFC3339), Message: "nk-deploy running"})
		cmd := exec.Command(cfg.upgrade, "nk-deploy", tarPath)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			writeState(cfg, updateState{State: "failed", Version: m.Version,
				Started: time.Now().Format(time.RFC3339), Finished: time.Now().Format(time.RFC3339), Error: err.Error()})
			fmt.Fprintln(os.Stderr, "部署失败:", err)
			return 1
		}
		writeState(cfg, updateState{State: "success", Version: m.Version,
			Started: time.Now().Format(time.RFC3339), Finished: time.Now().Format(time.RFC3339), Message: "deployed"})
		fmt.Println("deployed", m.Version)
		return 0
	}
	unit := newUnit()
	if err := writeState(cfg, updateState{State: "starting", Version: m.Version, Unit: unit,
		Started: time.Now().Format(time.RFC3339)}); err != nil {
		fmt.Fprintln(os.Stderr, "写状态失败:", err)
		return 1
	}
	if err := startUnit(cfg, unit, selfPath(), "run-deploy", tarPath, m.Version, unit); err != nil {
		writeState(cfg, updateState{State: "failed", Version: m.Version, Unit: unit,
			Started: time.Now().Format(time.RFC3339), Finished: time.Now().Format(time.RFC3339), Error: err.Error()})
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("update started: %s\n", unit)
	return 0
}

func cmdRollback(cfg config, foreground bool) int {
	if !isRoot() {
		fmt.Fprintln(os.Stderr, "rollback 需要 root")
		return 1
	}
	if foreground {
		writeState(cfg, updateState{State: "rolling_back",
			Started: time.Now().Format(time.RFC3339), Message: "nk-rollback running"})
		cmd := exec.Command(cfg.upgrade, "nk-rollback")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		err := cmd.Run()
		st := updateState{State: "rolled_back", Started: time.Now().Format(time.RFC3339),
			Finished: time.Now().Format(time.RFC3339), Message: "rolled back"}
		if err != nil {
			st.State = "failed"
			st.Error = err.Error()
		}
		writeState(cfg, st)
		return exitCode(err)
	}
	unit := newUnit()
	writeState(cfg, updateState{State: "rolling_back", Unit: unit,
		Started: time.Now().Format(time.RFC3339), Message: "nk-rollback running"})
	if err := startUnit(cfg, unit, selfPath(), "run-rollback", unit); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("rollback started: %s\n", unit)
	return 0
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	fmt.Fprintln(os.Stderr, err)
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode()
	}
	return 1
}

func usage() {
	fmt.Fprintf(os.Stderr, `用法:
  nokeyout-update check
  nokeyout-update status
  nokeyout-update install [--foreground] [version]
  nokeyout-update rollback [--foreground]

环境:
  NOKEYOUT_RELEASE_URL        manifest 地址（默认 %s）
  NOKEYOUT_RELEASE_PUBKEY     发布公钥（默认 %s）
  NOKEYOUT_UPDATE_DIR         下载暂存目录
  NOKEYOUT_UPGRADE_BIN        nk-deploy wrapper
`, defaultManifest, defaultPubKey)
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cfg := loadConfig()
	switch os.Args[1] {
	case "check":
		os.Exit(cmdCheck(cfg))
	case "status":
		os.Exit(cmdStatus(cfg))
	case "install":
		fs := flag.NewFlagSet("install", flag.ExitOnError)
		fg := fs.Bool("foreground", false, "同步执行 nk-deploy（默认 systemd-run 独立 unit）")
		fs.Parse(os.Args[2:])
		os.Exit(cmdInstall(cfg, fs.Args(), *fg))
	case "rollback":
		fs := flag.NewFlagSet("rollback", flag.ExitOnError)
		fg := fs.Bool("foreground", false, "同步执行 nk-rollback")
		fs.Parse(os.Args[2:])
		os.Exit(cmdRollback(cfg, *fg))
	case "run-deploy":
		os.Exit(cmdRunDeploy(cfg, os.Args[2:]))
	case "run-rollback":
		os.Exit(cmdRunRollback(cfg, os.Args[2:]))
	case "version":
		fmt.Println("0.1.0")
	default:
		usage()
		os.Exit(2)
	}
}
