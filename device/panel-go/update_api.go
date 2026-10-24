//go:build !(js && wasm)

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os/exec"
	"time"
)

const updateBin = "/usr/local/bin/nokeyout-update"

func runUpdateCmd(root bool, timeout time.Duration, args ...string) (int, string, string) {
	argv := []string{updateBin}
	if root && nonRoot() {
		argv = append([]string{"sudo", "-n"}, argv...)
	}
	argv = append(argv, args...)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, argv[0], argv[1:]...).CombinedOutput()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode(), string(out), ""
		}
		return 127, "", err.Error()
	}
	return 0, string(out), ""
}

func (p *panel) updateStatus(w http.ResponseWriter, r *http.Request) {
	if p.user(r) == "" {
		p.errJSON(w, r, 401, "no session")
		return
	}
	rc, out, errs := runUpdateCmd(false, 10*time.Second, "status")
	if rc != 0 {
		p.errJSON(w, r, 502, firstNonEmpty(errs, out, "nokeyout-update status failed"))
		return
	}
	var v any
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		p.errJSON(w, r, 502, "nokeyout-update status 返回非 JSON")
		return
	}
	p.json(w, r, 200, v, nil)
}

func (p *panel) updateCheck(w http.ResponseWriter, r *http.Request) {
	if p.user(r) == "" {
		p.errJSON(w, r, 401, "no session")
		return
	}
	rc, out, errs := runUpdateCmd(false, 60*time.Second, "check")
	if rc != 0 {
		p.errJSON(w, r, 502, firstNonEmpty(errs, out, "check failed"))
		return
	}
	var v any
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		p.errJSON(w, r, 502, "check 返回非 JSON")
		return
	}
	p.json(w, r, 200, v, nil)
}

func (p *panel) updateInstall(w http.ResponseWriter, r *http.Request) {
	if p.user(r) == "" {
		p.errJSON(w, r, 401, "no session")
		return
	}
	rc, out, errs := runUpdateCmd(true, 900*time.Second, "install")
	if rc != 0 {
		p.errJSON(w, r, 502, firstNonEmpty(errs, out, "install failed"))
		return
	}
	p.json(w, r, 202, map[string]any{"ok": true, "out": out}, nil)
}

func (p *panel) updateRollback(w http.ResponseWriter, r *http.Request) {
	if p.user(r) == "" {
		p.errJSON(w, r, 401, "no session")
		return
	}
	rc, out, errs := runUpdateCmd(true, 300*time.Second, "rollback")
	if rc != 0 {
		p.errJSON(w, r, 502, firstNonEmpty(errs, out, "rollback failed"))
		return
	}
	p.json(w, r, 202, map[string]any{"ok": true, "out": out}, nil)
}
