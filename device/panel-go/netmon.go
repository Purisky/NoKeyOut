//go:build !(js && wasm)

package main

// netmon.go —— 设备网络状态（屏头 ETH/NET 指示灯的数据源）
// 进程内轮询，15s 一拍；结果并入 stats.snapshot() 的 "net" 字段：
//   {"eth":"up"|"down"|"unknown","inet":true|false,"age_s":N}
// ETH = /sys/class/net/<if>/carrier；NET = 到公共任播 IP 的 TCP 连通（不经 DNS，
// 也不依赖上游代理配置；设备出网走 mihomo 透明代理时同样测得通）。

import (
	"net"
	"os"
	"strings"
	"sync"
	"time"
)

type netMon struct {
	mu       sync.Mutex
	eth      string
	inet     bool
	ts       time.Time
	checking bool
}

var netmon = &netMon{eth: "unknown"}

func (n *netMon) snapshot() map[string]any {
	n.mu.Lock()
	defer n.mu.Unlock()
	age := -1.0
	if !n.ts.IsZero() {
		age = time.Since(n.ts).Seconds()
	}
	return map[string]any{"eth": n.eth, "inet": n.inet, "age_s": age, "checking": n.checking}
}

// defaultIface：默认路由接口（/proc/net/route 里 Destination=00000000 那条）。
// 不写死 eth0——可预测网卡名（enP3p49s0）在 RK3588 等板子上是常态。
func defaultIface() string {
	b, err := os.ReadFile("/proc/net/route")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n")[1:] {
		f := strings.Fields(line)
		if len(f) >= 2 && f[1] == "00000000" {
			return f[0]
		}
	}
	return ""
}

func (n *netMon) ethUp(ifname string) string {
	if ifname == "" {
		ifname = defaultIface()
	}
	if ifname == "" {
		return "unknown"
	}
	b, err := os.ReadFile("/sys/class/net/" + ifname + "/carrier")
	if err != nil {
		// carrier 读不到（接口不存在/禁用）→ operstate 兜底
		if b2, err2 := os.ReadFile("/sys/class/net/" + ifname + "/operstate"); err2 == nil {
			if strings.HasPrefix(strings.TrimSpace(string(b2)), "up") {
				return "up"
			}
		}
		return "unknown"
	}
	if strings.TrimSpace(string(b)) == "1" {
		return "up"
	}
	return "down"
}

func (n *netMon) inetUp() bool {
	// 任播 IP:443，任一可达即算通（避免依赖 DNS/具体站点）
	for _, addr := range []string{"223.5.5.5:443", "1.1.1.1:443"} {
		c, err := net.DialTimeout("tcp", addr, 2*time.Second)
		if err == nil {
			_ = c.Close()
			return true
		}
	}
	return false
}

func (n *netMon) tick(ifname string) {
	n.mu.Lock()
	n.checking = true
	n.mu.Unlock()
	eth := n.ethUp(ifname)
	inet := n.inetUp()
	n.mu.Lock()
	n.eth, n.inet, n.ts, n.checking = eth, inet, time.Now(), false
	n.mu.Unlock()
}

func startNetmon(ifname string) {
	if ifname == "auto" {
		ifname = "" // 空 = 每次 tick 现探测默认路由接口（网卡改名/多网口自适应）
	}
	go func() {
		netmon.tick(ifname) // 启动即采一次
		for range time.Tick(15 * time.Second) {
			netmon.tick(ifname)
		}
	}()
}
