//go:build !(js && wasm)

package main

// auth.go —— PAM 校验（root helper）、会话（HMAC cookie）、登录限速

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

var userRE = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

// pamVerify：True=通过 False=拒绝 None=后端不可用（调用方按拒绝处理并记日志）。
// 只走 root helper：面板生产以 nokeyout（非 root）运行，unix_chkpwd 的 setuid
// helper 硬约束只有 root 或本人能调，故 helper 是唯一生产路径；缺失=拒绝（fail closed）。
func pamVerify(user, password string) *bool {
	if !userRE.MatchString(user) {
		f := false
		return &f
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sudo", "-n", authHelper)
	cmd.Stdin = strings.NewReader(user + "\n" + password + "\n")
	err := cmd.Run()
	var ee *exec.ExitError
	switch {
	case err == nil:
		t := true
		return &t
	case errors.As(err, &ee):
		if ee.ExitCode() == 0 {
			t := true
			return &t
		}
		if ee.ExitCode() == 1 {
			f := false
			return &f
		}
		return nil // 2 = 后端问题（helper 缺失 / pam 未装）
	default:
		return nil
	}
}

// verifyPassword：True=通过 False=拒绝 None=后端不可用（None 也按拒绝处理）。
func verifyPassword(user, password string) *bool {
	v := pamVerify(user, password)
	if v != nil {
		return v
	}
	log.Printf("auth backend unavailable for user=%q", user)
	return nil
}

// ---------------------------------------------------------------- sessions

type sessionEntry struct {
	user string
	exp  time.Time
}

type sessions struct {
	ttl time.Duration
	mu  sync.Mutex
	d   map[string]sessionEntry
}

func newSessions(ttlSec int) *sessions {
	return &sessions{ttl: time.Duration(ttlSec) * time.Second, d: map[string]sessionEntry{}}
}

func (s *sessions) sign(sid, secret []byte) string {
	m := hmac.New(sha256.New, secret)
	m.Write(sid)
	return hex.EncodeToString(m.Sum(nil))[:24]
}

func (s *sessions) create(user string, secret []byte) string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	sid := hex.EncodeToString(b) // 24 bytes → 48 hex chars（与原 token_urlsafe(24) 等价强度）
	s.mu.Lock()
	s.d[sid] = sessionEntry{user: user, exp: time.Now().Add(s.ttl)}
	s.mu.Unlock()
	return sid + "." + s.sign([]byte(sid), secret)
}

func (s *sessions) user(cookie string, secret []byte) string {
	if cookie == "" {
		return ""
	}
	sid, sig, found := strings.Cut(cookie, ".")
	if !found {
		return ""
	}
	if !hmac.Equal([]byte(sig), []byte(s.sign([]byte(sid), secret))) {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.d[sid]
	if !ok || e.exp.Before(time.Now()) {
		delete(s.d, sid)
		return ""
	}
	return e.user
}

func (s *sessions) drop(cookie string) {
	if cookie != "" {
		sid, _, _ := strings.Cut(cookie, ".")
		s.mu.Lock()
		delete(s.d, sid)
		s.mu.Unlock()
	}
}

// dropAll：清空全部会话（锁定时轮换 session secret 用）。
func (s *sessions) dropAll() {
	s.mu.Lock()
	s.d = map[string]sessionEntry{}
	s.mu.Unlock()
}

// ---------------------------------------------------------------- rate limit

// rateLimit：渐进 sleep 计数。默认**不再硬锁**——"N 次锁 M 分钟"本身是可被利用的
// DoS（LAN 上任何人故意输错就能把管理员反复锁在门外）；
type rateLimit struct {
	max   int
	win   time.Duration
	ban   bool
	mu    sync.Mutex
	fails map[string][2]float64 // [次数, 锁定截止 unix 秒]
}

func newRateLimit(max int, winSec int) *rateLimit {
	return &rateLimit{max: max, win: time.Duration(winSec) * time.Second,
		ban: os.Getenv("PANEL_LOGIN_BAN") == "1", fails: map[string][2]float64{}}
}

func (r *rateLimit) check(ip string) bool {
	if !r.ban {
		return true
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.fails[ip]
	return !(ok && e[1] > float64(time.Now().Unix()))
}

// penalty：本次失败应睡的秒数（0.4s 起步，每 5 次 +0.4s，上限 2s）。
func (r *rateLimit) penalty(ip string) time.Duration {
	r.mu.Lock()
	n := int(r.fails[ip][0])
	r.mu.Unlock()
	d := 400*time.Millisecond + 400*time.Millisecond*time.Duration(n/5)
	if d > 2*time.Second {
		d = 2 * time.Second
	}
	return d
}

func (r *rateLimit) fail(ip string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e := r.fails[ip]
	e[0]++
	if r.ban && int(e[0]) >= r.max {
		e[1] = float64(time.Now().Add(r.win).Unix())
	}
	r.fails[ip] = e
}

func (r *rateLimit) ok(ip string) {
	r.mu.Lock()
	delete(r.fails, ip)
	r.mu.Unlock()
}
