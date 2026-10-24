#!/usr/bin/env python3
"""logobake.py —— 把供应商图标烘成 2bpp 掩码，生成 device/panel-go/screen_logo_gen.go。

为什么走"掩码"而不是整张彩色位图：
  · 复用现有 `Canvas.bitmapAA`（2bpp 覆盖度 + 真 alpha 合成），不需要新渲染原语；
  · 与字体同一套格式，行内 16px 下线条干净；
  · 颜色仍由代码给（品牌色/前景色），不会和状态色（绿/黄/红）抢语义。

光栅化用**本机 Chrome 无头**（SVG → PNG），避免引入 cairo/inkscape 依赖：
先把所有图标排成一条竖直条一次截屏，再按格切片——比逐个启动浏览器快得多。

用法：
  python logobake.py --svg-dir logos/svg --out ../panel-go/screen_logo_gen.go --size 16

约定：svn 目录里 `foo.svg` 生成条目 "foo"；Go 侧用 provider id 做子串匹配（见 logoFor）。
"""

import argparse
import os
import subprocess
import sys
import tempfile

from PIL import Image

# 无头 Chrome 位置（Windows / macOS / Linux 常见路径）
CHROME_CANDIDATES = [
    r"C:\Program Files\Google\Chrome\Application\chrome.exe",
    r"C:\Program Files (x86)\Google\Chrome\Application\chrome.exe",
    "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
    "/usr/bin/google-chrome",
    "/usr/bin/chromium",
    "/usr/bin/chromium-browser",
]


def find_chrome(explicit):
    if explicit:
        return explicit
    for p in CHROME_CANDIDATES:
        if os.path.exists(p):
            return p
    sys.exit("找不到 Chrome；用 --chrome 指定可执行文件路径")


def render_strip(chrome, svg_paths, size):
    """把所有 SVG 排成 size × (size*n) 的竖直条，一次截屏；返回 PIL Image（带 alpha）。"""
    cells = []
    for p in svg_paths:
        uri = "file:///" + os.path.abspath(p).replace("\\", "/").replace("#", "%23")
        cells.append(
            f'<div style="width:{size}px;height:{size}px;overflow:hidden">'
            f'<img src="{uri}" style="width:{size}px;height:{size}px;object-fit:contain"></div>'
        )
    html = (
        '<html><head><meta charset="utf-8"><style>'
        "html,body{margin:0;padding:0;background:transparent}"
        "img{display:block;image-rendering:auto}"
        "</style></head><body>" + "".join(cells) + "</body></html>"
    )
    with tempfile.TemporaryDirectory() as td:
        hp = os.path.join(td, "strip.html")
        op = os.path.join(td, "strip.png")
        with open(hp, "w", encoding="utf-8") as f:
            f.write(html)
        cmd = [
            chrome,
            "--headless=new",
            "--disable-gpu",
            "--hide-scrollbars",
            "--force-device-scale-factor=1",
            "--default-background-color=00000000",
            f"--window-size={size},{size * len(svg_paths)}",
            f"--screenshot={op}",
            "file:///" + hp.replace("\\", "/"),
        ]
        subprocess.run(cmd, check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        return Image.open(op).convert("RGBA").copy()


def alpha_grid(img, size, idx):
    """取第 idx 格，返回 (w,h,alpha[y][x])。背景透明则直接用 alpha；
    若被渲染成不透明底（白），按"非白即墨"估算覆盖度。"""
    x0, y0 = 0, idx * size
    cell = img.crop((x0, y0, x0 + size, y0 + size))
    px = cell.load()
    opaque = cell.getchannel("A").getextrema()[0] > 0  # 有完全不透明像素 → 当白底处理
    white = all(px[x, y][3] == 255 for y in range(size) for x in range(size))
    grid = []
    for y in range(size):
        row = []
        for x in range(size):
            r, g, b, a = px[x, y]
            if white or (opaque and a == 255):
                # 白底：用亮度反推覆盖度
                lum = (r * 299 + g * 587 + b * 114) // 1000
                row.append(max(0, 255 - lum))
            else:
                row.append(a)
        grid.append(row)
    return size, size, grid


def pack_1bpp(grid, w, h):
    """1bpp 行位图（每字节 8 列、高位在左）：阈值 128 → 硬像素（点阵风）。"""
    rb = (w + 7) // 8
    out = bytearray(rb * h)
    for y in range(h):
        for x in range(w):
            if grid[y][x] < 128:
                continue
            out[y * rb + x // 8] |= 0x80 >> (x % 8)
    return bytes(out)


def pack_2bpp(grid, w, h):
    rb = (w + 3) // 4
    out = bytearray(rb * h)
    for y in range(h):
        for x in range(w):
            a = grid[y][x]
            if a == 0:
                continue
            lvl = min(3, (a * 3 + 127) // 255)
            if lvl == 0:
                continue
            out[y * rb + x // 4] |= lvl << (6 - 2 * (x % 4))
    return bytes(out)


def ink_bbox(grid, w, h):
    xs, ys = [], []
    for y in range(h):
        for x in range(w):
            if grid[y][x] > 32:
                xs.append(x)
                ys.append(y)
    if not xs:
        return None
    return min(xs), min(ys), max(xs) + 1, max(ys) + 1


def crop_and_pad(grid, w, h, box, size):
    """把墨迹按等比缩放到 size 内并居中（图标各家留白不同，统一视觉大小）。"""
    x0, y0, x1, y1 = box
    iw, ih = x1 - x0, y1 - y0
    scale = min(size / iw, size / ih)
    nw, nh = max(1, int(round(iw * scale))), max(1, int(round(ih * scale)))
    out = [[0] * size for _ in range(size)]
    ox, oy = (size - nw) // 2, (size - nh) // 2
    for y in range(nh):
        sy = y0 + min(ih - 1, int(y / scale))
        for x in range(nw):
            sx = x0 + min(iw - 1, int(x / scale))
            out[oy + y][ox + x] = grid[sy][sx]
    return out


def go_bytes(name, blob, per_line=16):
    rows = ["var %s = []byte{" % name]
    for i in range(0, len(blob), per_line):
        rows.append("\t" + " ".join("0x%02x," % b for b in blob[i : i + per_line]))
    rows.append("}")
    return "\n".join(rows)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--svg-dir", required=True)
    ap.add_argument("--out", required=True)
    ap.add_argument("--size", type=int, default=16)
    ap.add_argument("--chrome")
    ap.add_argument("--aa", type=int, default=1,
                    help="1=2bpp 覆盖度（抗锯齿），0=1bpp 硬像素（点阵风，与像素字体一致）")
    ap.add_argument("--sizes", default="16",
                    help="逗号分隔的尺寸列表，如 16,24（每个尺寸一套 mask，供不同密度的行使用）")
    args = ap.parse_args()

    names = sorted(
        os.path.splitext(f)[0]
        for f in os.listdir(args.svg_dir)
        if os.path.splitext(f)[1].lower() in (".svg", ".png", ".ico")
    )
    if not names:
        sys.exit("目录里没有 .svg/.png/.ico：" + args.svg_dir)
    paths = []
    for n in names:
        for ext in (".svg", ".png", ".ico"):
            p = os.path.join(args.svg_dir, n + ext)
            if os.path.exists(p):
                paths.append(p)
                break

    sizes = [int(x) for x in str(args.sizes).split(",") if x.strip()]
    chrome = find_chrome(args.chrome)
    by_size = {}   # size -> [(name, blob)]
    for size in sizes:
        img = render_strip(chrome, paths, size)
        sprites, empties = [], []
        for i, name in enumerate(names):
            w, h, grid = alpha_grid(img, size, i)
            box = ink_bbox(grid, w, h)
            if box is None:
                empties.append(name)
                continue
            fitted = crop_and_pad(grid, w, h, box, size)
            blob = pack_2bpp(fitted, size, size) if args.aa else pack_1bpp(fitted, size, size)
            sprites.append((name, blob))
        if empties:
            print("警告 %dpx：这些图标渲染为空，已跳过: %s" % (size, ", ".join(empties)), file=sys.stderr)
        if not sprites:
            sys.exit("所有图标都是空的——检查 Chrome 是否真的渲染了 SVG")
        by_size[size] = sprites
    sprites = by_size[sizes[0]]

    parts = [
        "// screen_logo_gen.go —— 供应商图标掩码（自动生成，勿手改）",
        "// 生成：device/tools/logobake/logobake.py --sizes %s --aa %d" % (args.sizes, args.aa),
        "// 格式：%s" % ("1bpp 行位图（Canvas.bitmap，硬像素/点阵风）" if not args.aa
                        else "2bpp 覆盖度（Canvas.bitmapAA）"),
        "package main",
        "",
    ]
    for size in sizes:
        for name, blob in by_size[size]:
            parts.append(go_bytes("logo%d_%s" % (size, name.replace("-", "_").replace(".", "_")), blob))
            parts.append("")
    for size in sizes:
        parts.append("// logoSprites%d：key → %dpx 1bpp 掩码（用 Canvas.bitmap 画）。" % (size, size))
        parts.append("var logoSprites%d = map[string][]byte{" % size)
        for name, blob in by_size[size]:
            parts.append('\t"%s": logo%d_%s,' % (name, size, name.replace("-", "_").replace(".", "_")))
        parts.append("}")
        parts.append("")
    parts.append("// logoSprites：默认尺寸（%dpx）的掩码表，兼容旧引用。" % sizes[0])
    parts.append("var logoSprites = logoSprites%d" % sizes[0])

    with open(args.out, "w", encoding="utf-8") as f:
        f.write("\n".join(parts))

    print(
        "baked %d logos @%s → %s（%s 模式）"
        % (len(by_size[sizes[0]]), args.sizes, args.out,
           "2bpp" if args.aa else "1bpp 硬像素"),
        file=sys.stderr,
    )



if __name__ == "__main__":
    main()
