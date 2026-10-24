#!/usr/bin/env python3
"""bake.py —— 生成 device/panel-go/screen_gen.go（240×240 位图字体）。

仓库此前**没有留下烘焙工具**，导致字号改不了、中文缺字（「日/月/重置」等一律显示成 □）。

输出格式必须与 `screen_font.go` 的 `newScreenFont` 完全一致：

  定宽字体（sm/md/cjk）
    - 每字格 w = adv-1，盒高 h；1bpp 行位图（rowbytes=(w+7)/8，高位在左，h 行）
    - 2bpp 覆盖度（crows=(w+3)/4，每字节 4 像素、高位在前；级别 0/1/2/3 → 0/85/170/255）
    - data/offsets 按「字符顺序」连续排布：offsets[i] = i*rowbytes*h
  变宽字体（num）
    - 每字宽度按墨迹紧排，widths/offsets/covOffs 显式给出；基线对齐（ascent）

用法：
  python bake.py --jb <JetBrainsMono 目录> --cjk <NotoSansSC.ttf> --out <screen_gen.go> [--scan <Go 源码目录>]
"""

import argparse
import glob
import os
import re
import sys

from PIL import Image, ImageFont

# 与前版保持一致的 ASCII 字集（含省略号）。
# 末尾补**货币符号**：`¥`（人民币）/`£`（英镑）在 12px 下正好一个等宽格；
# `€` 在源字体里是双格宽，烘焙器按单格切会缺角，所以不放（代码里退回单位代码 EUR）。
ASCII_CHARS = "".join(chr(c) for c in range(0x20, 0x7F)) + "…¥£"
NUM_CHARS = "0123456789.-%"

# 中文补齐：除扫描 Go 源码里出现的 CJK，再显式补这些常用标签字，
# 免得改文案时又出现 □（改文案只需要重跑本工具）。
CJK_EXTRA = (
    "剩余余重置配额已用供应商窗口小时天周月日计划套餐额可用量期限同步失败等级"
    "警告危急正常总计平均最高低当前历史趋势上下左右第共个条项次分秒时"
    "主屏额度池质量锁定开放数据秒前无输出入调用错误今日缓存命中率速近两分钟"
    "面板不可达网关管理已阻隔拨到或拔线解模型行影响缺□正常未就绪检查令牌峰值期新鲜与受"
)


def cjk_set_from_sources(scan_dirs):
    """扫描 Go 源码字符串字面量里的 CJK（保证「界面真的用到的字」都在）。"""
    chars = set(CJK_EXTRA)
    for d in scan_dirs:
        for path in glob.glob(os.path.join(d, "*.go")):
            try:
                src = open(path, encoding="utf-8").read()
            except OSError:
                continue
            for ch in src:
                if "\u4e00" <= ch <= "\u9fff" or ch in "，。：（）「」／":
                    chars.add(ch)
    # □ = screenTofu，必须在表里（loader 会查它）
    chars.add("□")
    return "".join(sorted(chars))


def alpha_grid(font, ch):
    """返回 (w, h, alpha[y][x], bbox)。alpha 为 0..255。"""
    bbox = font.getbbox(ch)  # (left, top, right, bottom)，原点在 ascender 顶
    mask = font.getmask(ch, mode="L")  # PIL ≥10 返回 ImagingCore
    w, h = mask.size
    if w == 0 or h == 0:
        return 0, 0, [], bbox
    img = Image.frombytes("L", (w, h), bytes(mask))
    px = img.load()
    alpha = [[px[x, y] for x in range(w)] for y in range(h)]
    return w, h, alpha, bbox


def pack_1bpp(alpha, w, h, ox, oy, box_w, box_h):
    """把墨迹贴进 box 的 (ox,oy)，输出 1bpp 行位图（rowbytes=(box_w+7)//8）。"""
    rb = (box_w + 7) // 8
    out = bytearray(rb * box_h)
    for y in range(h):
        by = oy + y
        if by < 0 or by >= box_h:
            continue
        for x in range(w):
            bx = ox + x
            if bx < 0 or bx >= box_w or alpha[y][x] < 128:
                continue
            out[by * rb + bx // 8] |= 0x80 >> (bx % 8)
    return bytes(out)


def pack_2bpp(alpha, w, h, ox, oy, box_w, box_h):
    """2bpp 覆盖度：级别 = round(alpha*3/255)，每字节 4 像素、高位在前。"""
    rb = (box_w + 3) // 4
    out = bytearray(rb * box_h)
    for y in range(h):
        by = oy + y
        if by < 0 or by >= box_h:
            continue
        for x in range(w):
            bx = ox + x
            if bx < 0 or bx >= box_w:
                continue
            a = alpha[y][x]
            if a == 0:
                continue
            lvl = min(3, (a * 3 + 127) // 255)
            if lvl == 0:
                continue
            out[by * rb + bx // 4] |= lvl << (6 - 2 * (bx % 4))
    return bytes(out)


def bake_fixed(path, size, chars, mode, no_aa=False):
    """定宽烘焙（sm/md：基线对齐；cjk：盒内居中）。"""
    font = ImageFont.truetype(path, size)
    ascent, descent = font.getmetrics()

    # 字格宽度取**字集里最宽的 advance**，而不是拉丁 'M'：
    advs = []
    for ch in chars:
        m = font.getmask(ch, mode="L")
        if m.size[0] > 0:
            advs.append(font.getlength(ch))
    adv = int(round(max(advs))) + 1 if advs else int(round(font.getlength("M"))) + 1
    box_w = adv - 1

    # 先扫一遍求纵向包络，再定盒高与基线
    tops, bots = [], []
    for ch in chars:
        w, h, _, bbox = alpha_grid(font, ch)
        if w == 0:
            continue
        tops.append(bbox[1])
        bots.append(bbox[3])
    min_top = min(tops) if tops else 0
    max_bot = max(bots) if bots else ascent
    box_h = max_bot - min_top
    baseline = ascent - min_top  # 盒顶到基线的距离

    data, cov = bytearray(), bytearray()
    for ch in chars:
        w, h, alpha, bbox = alpha_grid(font, ch)
        if w == 0:
            data += bytes(((box_w + 7) // 8) * box_h)
            if not no_aa:  # no_aa 下 cov 必须**整段为空**：loader 靠 len(cov)==0 决定走 1bpp 路径
                cov += bytes(((box_w + 3) // 4) * box_h)
            continue
        if mode == "center":
            ox = (box_w - w) // 2
        else:  # baseline：按左侧留白摆放
            ox = bbox[0]
        oy = bbox[1] - min_top
        data += pack_1bpp(alpha, w, h, ox, oy, box_w, box_h)
        if not no_aa:
            cov += pack_2bpp(alpha, w, h, ox, oy, box_w, box_h)
    return {
        "h": box_h,
        "adv": adv,
        "ascent": baseline,
        "chars": chars,
        "data": bytes(data),
        "cov": bytes(cov),
    }


def bake_variable(path, size, chars, no_aa=False):
    """变宽烘焙（num）：每字按墨迹紧排 + 基线对齐。"""
    font = ImageFont.truetype(path, size)
    ascent, _ = font.getmetrics()
    glyphs = []
    for ch in chars:
        w, h, alpha, bbox = alpha_grid(font, ch)
        glyphs.append({"ch": ch, "w": w, "h": h, "alpha": alpha, "bbox": bbox})

    tops = [g["bbox"][1] for g in glyphs if g["w"]]
    bots = [g["bbox"][3] for g in glyphs if g["w"]]
    min_top = min(tops) if tops else 0
    max_bot = max(bots) if bots else ascent
    box_h = max_bot - min_top
    baseline = ascent - min_top

    data, cov = bytearray(), bytearray()
    widths, offsets, covoffs = [], [], []
    for g in glyphs:
        gw = g["w"] if g["w"] else 1
        widths.append(gw)
        offsets.append(len(data))
        covoffs.append(len(cov))
        if g["w"] == 0:
            data += bytes(((gw + 7) // 8) * box_h)
            if not no_aa:
                cov += bytes(((gw + 3) // 4) * box_h)
            continue
        oy = g["bbox"][1] - min_top
        data += pack_1bpp(g["alpha"], g["w"], g["h"], 0, oy, gw, box_h)
        if not no_aa:
            cov += pack_2bpp(g["alpha"], g["w"], g["h"], 0, oy, gw, box_h)
    return {
        "h": box_h,
        "ascent": baseline,
        "chars": chars,
        "widths": widths,
        "offsets": offsets,
        "covoffs": covoffs,
        "data": bytes(data),
        "cov": bytes(cov),
    }


def go_bytes(name, blob, per_line=16):
    out = ["var %s = []byte{" % name]
    for i in range(0, len(blob), per_line):
        chunk = blob[i : i + per_line]
        out.append("\t" + " ".join("0x%02x," % b for b in chunk))
    out.append("}")
    return "\n".join(out)


def go_ints(name, vals):
    return "var %s = []int{%s}" % (name, ", ".join(str(v) for v in vals))


def go_str(s):
    # Go 双引号字面量：转义 \ 与 "
    return '"' + s.replace("\\", "\\\\").replace('"', '\\"') + '"'


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--jb", required=True, help="含 JetBrainsMono-*.ttf 的目录")
    ap.add_argument("--cjk", required=True, help="Noto Sans SC ttf")
    ap.add_argument("--out", required=True, help="输出 screen_gen.go")
    ap.add_argument("--scan", action="append", default=[], help="扫描 CJK 的 Go 源码目录")
    ap.add_argument("--num-file", default="JetBrainsMono-ExtraBold.ttf",
                    help="num 字阶的字体文件（放在 --jb 目录里）")
    ap.add_argument("--sm-file", default="JetBrainsMono-Regular.ttf",
                    help="sm 字阶（小标签）字体文件")
    ap.add_argument("--md-file", default="JetBrainsMono-Medium.ttf",
                    help="md 字阶（标题/时钟）字体文件")
    ap.add_argument("--num-size", type=int, default=36)
    ap.add_argument("--sm-size", type=int, default=13)
    ap.add_argument("--md-size", type=int, default=15)
    ap.add_argument("--cjk-size", type=int, default=15)
    ap.add_argument("--no-aa", action="store_true",
                    help="只烘 1bpp 硬像素（点阵字体的正路）：cov 留空，设备侧走无抗锯齿路径")
    args = ap.parse_args()

    jb = lambda n: os.path.join(args.jb, n)
    sm = bake_fixed(jb(args.sm_file), args.sm_size, ASCII_CHARS, "baseline", args.no_aa)
    md = bake_fixed(jb(args.md_file), args.md_size, ASCII_CHARS, "baseline", args.no_aa)
    num = bake_variable(jb(args.num_file), args.num_size, NUM_CHARS, args.no_aa)
    cjk_chars = cjk_set_from_sources(args.scan)
    cjk = bake_fixed(args.cjk, args.cjk_size, cjk_chars, "center", args.no_aa)

    parts = [
        "// screen_gen.go —— 240×240 位图字体数据（自动生成，勿手改）",
        "// 生成：device/tools/fontbake/bake.py（JetBrains Mono OFL + Noto Sans SC OFL）",
        "// 字阶：sm %dpx(%d×%d) / md %dpx(%d×%d) / num %dpx(%d) / cjk %dpx(%d×%d)"
        % (
            args.sm_size, sm["adv"] - 1, sm["h"],
            args.md_size, md["adv"] - 1, md["h"],
            args.num_size, num["h"],
            args.cjk_size, cjk["adv"] - 1, cjk["h"],
        ),
        "package main",
        "",
    ]
    for key, f in (("sm", sm), ("md", md), ("num", num), ("cjk", cjk)):
        cap = key.capitalize()
        parts.append(go_bytes(key + "FontData", f["data"]))
        parts.append("")
        parts.append(go_bytes(key + "FontCov", f["cov"]))
        parts.append("")
        if "widths" in f:
            parts.append(go_ints("numFontWidths", f["widths"]))
            parts.append(go_ints("numFontOffsets", f["offsets"]))
            parts.append(go_ints("numFontCovOffs", f["covoffs"]))
            parts.append("")
        _ = cap
    parts.append("var fontDefs = []fontDef{")
    # 定宽字体不写 ascent：Ascent>0 会把 draw 的 y 当成基线，
    # 而现有版式一律按「y = 盒顶」调用（基线位置已烘焙进字模行）。
    parts.append(
        '\t{key: "sm", h: %d, adv: %d, chars: %s, data: smFontData, cov: smFontCov},'
        % (sm["h"], sm["adv"], go_str(sm["chars"]))
    )
    parts.append(
        '\t{key: "md", h: %d, adv: %d, chars: %s, data: mdFontData, cov: mdFontCov},'
        % (md["h"], md["adv"], go_str(md["chars"]))
    )
    parts.append(
        '\t{key: "num", h: %d, ascent: %d, variable: true, chars: %s, widths: numFontWidths, offsets: numFontOffsets, data: numFontData, cov: numFontCov, covOffs: numFontCovOffs},'
        % (num["h"], num["ascent"], go_str(num["chars"]))
    )
    parts.append(
        '\t{key: "cjk", h: %d, adv: %d, chars: %s, data: cjkFontData, cov: cjkFontCov},'
        % (cjk["h"], cjk["adv"], go_str(cjk["chars"]))
    )
    parts.append("}")
    parts.append("")

    with open(args.out, "w", encoding="utf-8") as fp:
        fp.write("\n".join(parts))

    print(
        "baked: sm %dx%d adv%d | md %dx%d adv%d | num h%d %d glyphs | cjk %dx%d adv%d %d glyphs"
        % (
            sm["adv"] - 1, sm["h"], sm["adv"],
            md["adv"] - 1, md["h"], md["adv"],
            num["h"], len(num["chars"]),
            cjk["adv"] - 1, cjk["h"], cjk["adv"], len(cjk["chars"]),
        ),
        file=sys.stderr,
    )


if __name__ == "__main__":
    main()
