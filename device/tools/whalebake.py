#!/usr/bin/env python3
"""whalebake.py —— DeepSeek Harness 运行态鲸鱼尾 APNG → panel-go 2bpp 掩码。

源资产：deepseek-ai/deepseek-harness
  packages/client/ui-chat/src/client/assets/running-whale@2x.png
  MIT；28×28 APNG，60 帧 × 50ms 无限循环，灰 alpha 掩码（LA）。
本地副本：device/tools/whale/running-whale@2x.png

用法：
  python whalebake.py --png ../whale/running-whale@2x.png --out ../panel-go/screen_whale_gen.go

输出：28px（@2x 原尺寸）和 18px（简易行图标）两套 2bpp 覆盖度帧，
每帧行宽按 4px/字节对齐，与 Canvas.bitmapAA 的输入格式一致。
"""

import argparse
import sys

from PIL import Image, ImageFilter

FRAME_MS = 50
SIZES = (21, 15)


def coverage(frame, size):
    # 先按 2x 原尺寸膨胀 1px 再加粗笔画：小徽章上细线容易糊成一层灰。
    alpha = frame.getchannel("A") if frame.mode == "LA" else frame.convert("LA").getchannel("A")
    frame = Image.merge("LA", [alpha, alpha.filter(ImageFilter.MaxFilter(3))])
    if frame.size != (size, size):
        frame = frame.resize((size, size), Image.LANCZOS)
    alpha = frame.getchannel("A") if frame.mode == "LA" else frame.convert("LA")
    px = list(alpha.getdata())
    rb = (size + 3) // 4
    row = []
    for y in range(size):
        out = bytearray(rb)
        for x in range(size):
            a = px[y * size + x]
            q = (a * 3 + 127) // 255  # 0..3
            if q:
                out[x // 4] |= q << (6 - 2 * (x % 4))
        row.append(bytes(out))
    return b"".join(row)


def go_bytes(data, indent="\t"):
    lines = []
    for i in range(0, len(data), 16):
        lines.append(indent + ", ".join(f"0x{b:02x}" for b in data[i:i + 16]) + ",")
    return "\n".join(lines)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--png", required=True)
    ap.add_argument("--out", required=True)
    args = ap.parse_args()

    im = Image.open(args.png)
    n = getattr(im, "n_frames", 1)
    if n != 60:
        sys.exit(f"expected 60 frames, got {n}")
    frames = {s: [] for s in SIZES}
    for i in range(n):
        im.seek(i)
        dur = im.info.get("duration")
        if dur != FRAME_MS:
            sys.exit(f"frame {i}: duration {dur} != {FRAME_MS}")
        base = im.convert("LA")
        for s in SIZES:
            frames[s].append(coverage(base, s))

    with open(args.out, "w", encoding="utf-8", newline="\n") as f:
        f.write("package main\n\n")
        f.write("// screen_whale_gen.go —— DeepSeek Harness 运行态鲸鱼尾动画（60 帧 × 50ms，2bpp 掩码）。\n")
        f.write("// 由 device/tools/whalebake.py 从 device/tools/whale/running-whale@2x.png 生成，勿手改。\n\n")
        f.write(f"const whaleFrameMS = {FRAME_MS}\n\n")
        for s in SIZES:
            f.write(f"var whaleFrames{s} = [][]byte{{\n")
            for blob in frames[s]:
                f.write("\t{\n")
                f.write(go_bytes(blob, "\t\t"))
                f.write("\n\t},\n")
            f.write("}\n\n")
    print(f"wrote {args.out}: {n} frames × {', '.join(map(str, SIZES))}px")


if __name__ == "__main__":
    main()
