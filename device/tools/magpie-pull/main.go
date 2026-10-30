// magpie-pull —— 从 OCI Registry 直取镜像里的 /magpie 服务端二进制（替代 docker）
//
// 拉起整个守护进程（Buildroot/128MB 目标镜像不带 docker）；registry 协议本身
// 只是三个 HTTP 调用 + 一层 gzip tar 解包，不值得为一个 5MB 文件引入 25MB 客户端。
//
//   magpie-pull ghcr.io/yetone/magpie:0.1.2144 -o /tmp/magpie
//
// 匿名拉取（公开镜像）：token endpoint 取匿名 token → manifest（按本机 arch 选
// index 条目）→ blob（gzip tar）→ 提取 /magpie。输出前校验 ELF 魔数。
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"time"
)

const userAgent = "nokeyout-magpie-pull/1"

type tokenResp struct {
	Token string `json:"token"`
}

type manifest struct {
	MediaType string    `json:"mediaType"`
	Layers    []layer   `json:"layers"`
	Manifests []subMani `json:"manifests"` // index 才有
}

type layer struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
}

type subMani struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Platform  struct {
		Architecture string `json:"architecture"`
		OS           string `json:"OS"`
	} `json:"platform"`
}

func main() {
	arch := runtime.GOARCH
	osTarget := runtime.GOOS
	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		if args[i] == "-arch" && i+1 < len(args) {
			arch = args[i+1]
			args = append(args[:i], args[i+2:]...)
			break
		}
	}
	for i := 0; i < len(args); i++ {
		if args[i] == "-os" && i+1 < len(args) {
			osTarget = args[i+1]
			args = append(args[:i], args[i+2:]...)
			break
		}
	}
	if len(args) < 3 || args[1] != "-o" {
		fmt.Fprintln(os.Stderr, "用法: magpie-pull [-os linux] [-arch arm64] <image:tag> -o <输出路径>")
		os.Exit(2)
	}
	ref, out := args[0], args[2]
	reg, repo, tag := splitRef(ref)
	if reg == "" || repo == "" || tag == "" {
		fmt.Fprintln(os.Stderr, "镜像名需 registry/repo:tag 形式")
		os.Exit(2)
	}
	cl := newClient()

	// 1) 匿名 token（scope=repository:<repo>:pull）
	tok, err := anonToken(cl, reg, repo)
	if err != nil {
		fatal("token: %v", err)
	}

	// 2) manifest：index 则按本机 arch 选，直直接 manifest 则用之
	m, err := fetchManifest(cl, reg, repo, tag, tok, osTarget, arch)
	if err != nil {
		fatal("manifest: %v", err)
	}

	// 3) 逐层解 gzip tar，提取 /magpie（软链不 Dereference，取体积>1MB 的真实文件）
	for _, l := range m.Layers {
		if !strings.Contains(l.MediaType, "gzip") && !strings.Contains(l.MediaType, "tar") {
			continue
		}
		n, err := extractFromLayer(cl, reg, repo, l.Digest, tok, out)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  layer %s: %v\n", l.Digest[:19], err)
			continue
		}
		if n > 0 {
			if !isELF(out) {
				fatal("提取到的不是 ELF（%d 字节）", n)
			}
			fmt.Fprintf(os.Stderr, "✅ %s <- %s layer %s（%d 字节）\n", out, ref, l.Digest[:19], n)
			return
		}
	}
	fatal("镜像层里没找到 /magpie")
}

func fatal(f string, a ...any) {
	fmt.Fprintf(os.Stderr, "magpie-pull: "+f+"\n", a...)
	os.Exit(1)
}

// newClient：默认 10 分钟超时（单层可达百 MB）。代理：尊重 *_proxy 环境变量
// （Go 默认行为）；未设置时探测本机常见代理端口——国内设备直连 ghcr 常不可用，
// 而设备上普遍有本地代理（mihomo/clash）。NOKEYOUT_PROXY=url 显式指定，
// =off 禁用探测。
func newClient() *http.Client {
	tr := &http.Transport{Proxy: http.ProxyFromEnvironment}
	proxySet := os.Getenv("HTTP_PROXY") != "" || os.Getenv("HTTPS_PROXY") != "" ||
		os.Getenv("http_proxy") != "" || os.Getenv("https_proxy") != ""
	if !proxySet && os.Getenv("NOKEYOUT_PROXY") != "off" {
		port := ""
		if v := os.Getenv("NOKEYOUT_PROXY"); v != "" && v != "auto" {
			port = strings.TrimPrefix(v, "http://127.0.0.1:")
			port = strings.TrimPrefix(port, "127.0.0.1:")
		} else {
			for _, p := range []string{"7899", "7897", "7890", "1080"} {
				c, err := net.DialTimeout("tcp", "127.0.0.1:"+p, 200*time.Millisecond)
				if err == nil {
					c.Close()
					port = p
					break
				}
			}
		}
		if port != "" {
			u := "http://127.0.0.1:" + port
			if parsed, err := url.Parse(u); err == nil {
				tr.Proxy = http.ProxyURL(parsed)
				fmt.Fprintf(os.Stderr, "  使用本机代理 %s\n", u)
			}
		}
	}
	return &http.Client{Timeout: 10 * time.Minute, Transport: tr}
}

func splitRef(ref string) (reg, repo, tag string) {
	// 支持 ghcr.io/yetone/magpie:0.1.2144 与 docker.io/library/x:tag
	i := strings.Index(ref, "/")
	if i < 0 {
		return "", "", ""
	}
	reg, rest := ref[:i], ref[i+1:]
	// 只有一段 host 且不含点/冒号 → docker.io 短名（本工具只用 ghcr，从简处理）
	if !strings.ContainsAny(reg, ".:") {
		reg, rest = "docker.io", ref
	}
	if j := strings.LastIndex(rest, ":"); j > 0 && !strings.Contains(rest[j:], "/") {
		repo, tag = rest[:j], rest[j+1:]
	} else {
		repo, tag = rest, "latest"
	}
	return reg, repo, tag
}

func anonToken(cl *http.Client, reg, repo string) (string, error) {
	u := fmt.Sprintf("https://%s/token?scope=repository:%s:pull&service=%s", reg, repo, reg)
	resp, err := cl.Get(u)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var t tokenResp
	if json.Unmarshal(b, &t) != nil || t.Token == "" {
		return "", fmt.Errorf("token 响应异常: %s", string(b[:min(120, len(b))]))
	}
	return t.Token, nil
}

func fetchManifest(cl *http.Client, reg, repo, tag, tok, osTarget, arch string) (*manifest, error) {
	url := fmt.Sprintf("https://%s/v2/%s/manifests/%s", reg, repo, tag)
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", strings.Join([]string{
		"application/vnd.oci.image.index.v1+json",
		"application/vnd.docker.distribution.manifest.list.v2+json",
		"application/vnd.oci.image.manifest.v1+json",
		"application/vnd.docker.distribution.manifest.v2+json",
	}, ", "))
	resp, err := cl.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<22))
	var m manifest
	if json.Unmarshal(b, &m) != nil {
		return nil, fmt.Errorf("manifest 不是 JSON")
	}
	if len(m.Manifests) > 0 {
		// index：选本机 os/arch
		want := osTarget + "/" + arch // linux/arm64
		for _, sm := range m.Manifests {
			if sm.Platform.OS+"/"+sm.Platform.Architecture == want {
				return fetchManifest(cl, reg, repo, sm.Digest, tok, osTarget, arch)
			}
		}
		return nil, fmt.Errorf("index 里没有 %s（只有 %d 个条目）", want, len(m.Manifests))
	}
	if len(m.Layers) == 0 {
		return nil, fmt.Errorf("manifest 无层")
	}
	return &m, nil
}

// extractFromLayer：解一层，提取 /magpie（或 ./magpie/magpie）。返回写入字节数。
func extractFromLayer(cl *http.Client, reg, repo, digest, tok, out string) (int64, error) {
	url := fmt.Sprintf("https://%s/v2/%s/blobs/%s", reg, repo, digest)
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := cl.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return 0, fmt.Errorf("blob HTTP %d", resp.StatusCode)
	}
	gr, err := gzip.NewReader(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("层不是 gzip: %w", err)
	}
	tr := tar.NewReader(gr)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return 0, nil // 本层没有
		}
		if err != nil {
			return 0, err
		}
		if h.Typeflag == tar.TypeReg && (h.Name == "magpie" || h.Name == "./magpie" || h.Name == "/magpie") {
			f, err := os.Create(out)
			if err != nil {
				return 0, err
			}
			n, err := io.Copy(f, tr)
			f.Close()
			if err != nil {
				return 0, err
			}
			if err := os.Chmod(out, 0o755); err != nil {
				return 0, err
			}
			return n, nil
		}
	}
}

func isELF(p string) bool {
	b := make([]byte, 4)
	f, err := os.Open(p)
	if err != nil {
		return false
	}
	defer f.Close()
	_, err = io.ReadFull(f, b)
	return err == nil && bytes.Equal(b, []byte{0x7f, 'E', 'L', 'F'})
}
