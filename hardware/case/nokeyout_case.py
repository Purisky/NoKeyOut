#!/usr/bin/env python3
"""NoKeyOut 外壳：参数化生成器（CadQuery / OCCT 实体内核）。

布局（已定）：
  * 竖放方盒：屏幕朝前（−X），RJ45 / USB-A / USB-C 全部朝后（+X）；
  * 屏幕与 microSD 同一面：屏幕 z 12.9–41.5，TF 槽 z 5.0–9.0；
  * 整体壳（五面）+ 顶盖，摩擦裙边；
  * 屏幕 44×32 PCB 用 4×M2 自攻从内侧固定在前面板螺柱上；
  * 开关在 −Y 侧面靠上，两个侧按在 −Y 侧面下部。

板面数据直接解析 `web/diy/assets/board/neo3-plus-parts.js`（仓库唯一出处），
板子模型一变，开口自动跟着变。

板面坐标：左上角原点，x 向右、y 向下。CAD 坐标：
    X = bx − 24（+X = 背部），Y = 24 − by（+Y = 排针侧），Z 向上。

用法：
    python nokeyout_case.py               # 出 out/*.stl + assembly.step + 干涉检查
    python nokeyout_case.py --check       # 只检查
    python nokeyout_case.py --param base_h=50 --param screen_bottom=13
"""

from __future__ import annotations

import argparse
import json
import re
import sys
from dataclasses import dataclass, fields, replace
from pathlib import Path

import cadquery as cq
from cadquery import exporters

ROOT = Path(__file__).resolve().parents[2]
PARTS_JS = ROOT / "web" / "diy" / "assets" / "board" / "neo3-plus-parts.js"

# 器件离板面的高度（mm）。JS 里只有板面 x/y —— 这几个数请拿卡尺复核。
# 背板开口按"插头口"尺寸（宽 Y × 高 Z），不是元件包围盒：
# 元件本体在腔内，只有插头要穿过壁。
# 插头口：宽 Y × 高 Z × 圆角
# 每个口两套尺寸：
#   through = 外表面看到的通孔（按插头尺寸，可打磨扩大）
#   pocket  = 内侧让位腔（按连接器本体/护罩，深 1.6）
#   (通孔宽, 通孔高, 圆角, 口心相对板顶, 让位宽, 让位高)
PORT_SHAPE = {
    "LAN1":   (12.40, 9.00, 0.80, 5.00, 15.95, 16.05),   # RJ45 插头 11.68×8.3
    "USB1":   (12.30, 4.80, 0.60, 7.35, 13.60, 6.20),    # USB-A 插头 12×4.5
    "USB-C1": (8.60, 2.70, 1.35, 1.65, 9.40, 3.80),      # USB-C 插头 8.3×2.4
}

PART_HEIGHTS = {
    "CON30": 11.5, "LAN1": 13.5, "J3": 3.0, "USB1": 14.7, "USB-C1": 3.3,
    "CON18": 5.0, "CON31": 4.0, "BT1": 4.0, "K1": 3.3, "K2": 3.3,
    "D1": 0.7, "D2": 0.7, "CON": 2.54,
}


@dataclass(frozen=True)
class P:
    # 机壳
    clearance: float = 0.4      # 开口相对元件包围盒的单边放大
    floor: float = 2.0
    board_gap: float = 3.5      # 板底到内腔地面
    base_h: float = 45.5        # 底壳总高（不含盖）
    cavity_pad: float = 1.5     # 内腔到板边单边余量
    wall_side: float = 2.5
    wall_front: float = 3.4     # 前面板（SD 外伸 3.08 要过）
    wall_back: float = 3.0
    # 板螺柱
    boss_od: float = 5.6
    pilot: float = 2.5          # M3 自攻
    pilot_depth: float = 4.2
    # 盖板
    lid_t: float = 3.0
    led_hole: float = 2.0           # LED 观察孔（前脸裙边上）
    lip: float = 2.5
    lip_fit: float = 0.15       # 单边过盈
    lip_wall: float = 1.4
    # 屏幕（44×32 PCB，竖直装在前面板内侧）
    screen_w: float = 44.0
    screen_h: float = 32.0
    screen_active: float = 28.2
    screen_bottom: float = 11.8  # 屏幕 PCB 下沿 z
    screen_dy: float = 0.0       # 屏幕在 Y 上的偏移
    screen_t: float = 5.0        # 模块总厚（含玻璃）
    screen_boss: float = 4.4
    screen_boss_len: float = 0.8
    screen_hole_dx: float = 19.5  # 孔位相对 PCB 中心
    screen_hole_dy: float = 13.5
    screen_pilot: float = 1.6
    screen_pilot_depth: float = 1.8   # 从螺柱面再往板里钻 1.8
    # 屏幕托板（滑入前壁导轨，顶盖压住；所有屏幕螺丝在桌面上拧）
    panel_z0: float = 10.2          # 前面板下沿（以下是带 SD 槽的裙边）
    panel_rib: float = 1.2          # 侧滑槽筋宽/深
    carrier_w: float = 44.0         # 必须 ≤ 45.4：腔体圆角之外没有空间
    carrier_z: float = 11.0         # 托板下沿 z
    carrier_t: float = 1.2
    carrier_pocket: float = 1.2     # 托板沉进前壁的深度（视窗从 3.4 减到 2.2）
    carrier_corner: float = 3.0
    carrier_window: float = 30.0    # 大于玻璃 28.2，留定位余量
    carrier_pin_d: float = 2.0      # 两个压入前壁的定位销
    carrier_pin_len: float = 1.2
    carrier_pin_dy: float = 17.5
    carrier_pin_z: float = 27.8
    carrier_pocket_w: float = 44.6
    carrier_pocket_h: float = 34.0
    carrier_pocket_z: float = 27.8
    # 面板件
    switch_z: float = 30.0
    switch_body_l: float = 12.0     # 本体沿 X
    switch_body_d: float = 6.0      # 本体伸入腔内的深度
    switch_body_h: float = 6.0      # 本体沿 Z
    switch_knob_w: float = 4.0      # 拨柄沿 X
    switch_knob_h: float = 3.0      # 拨柄沿 Z
    switch_travel: float = 2.5      # 拨柄行程
    switch_slot_pad: float = 0.0    # 滑槽额外余量（实测 6.5 = 4 + 2.5）
    switch_sink: float = 1.0        # 内壁沉台深度：柄从 1.5mm 露到 2.5mm
    switch_pocket_w: float = 14.0   # 沉台沿 X
    switch_pocket_h: float = 9.0    # 沉台沿 Z
    switch_knob_off: float = 1.25   # 拨柄行程端点相对本体中心（居中 = travel/2）
    switch_slot_center: float = 0.0  # 滑槽中心，与本体中心对齐
    switch_play: float = 0.2        # 腔体在 Z / 深度方向的间隙
    switch_grip: float = 0.1        # X 方向单边过盈（压配，靠摩擦固定）
    button_hole: float = 4.2      # K2 用户键：加长杆用
    mask_hole: float = 1.8        # K1 MASK：只留针孔，不做按钮
    button_lip: float = 0.35


def load_board() -> dict:
    if not PARTS_JS.exists():
        sys.exit(f"找不到板面数据: {PARTS_JS}")
    m = re.search(r"window\.NK_BOARD_PARTS\s*=\s*(\{.*\});?\s*$",
                  PARTS_JS.read_text(encoding="utf-8"), re.S)
    if not m:
        sys.exit(f"无法解析 {PARTS_JS.name}")
    return json.loads(m.group(1))


def bx(x: float) -> float:
    return x - 24.0


def by(y: float) -> float:
    return 24.0 - y


def rprism(w: float, d: float, h: float, r: float, z0: float = 0.0) -> cq.Workplane:
    return cq.Workplane("XY").workplane(offset=z0).rect(w, d).extrude(h).edges("|Z").fillet(r)


def box(x0, x1, y0, y1, z0, z1) -> cq.Workplane:
    return (cq.Workplane("XY").box(abs(x1 - x0), abs(y1 - y0), abs(z1 - z0))
            .translate(((x0 + x1) / 2, (y0 + y1) / 2, (z0 + z1) / 2)))


def cyl(center, r: float, h: float, vec=(0, 0, 1)) -> cq.Workplane:
    return cq.Workplane(obj=cq.Solid.makeCylinder(r, h, cq.Vector(*center), cq.Vector(*vec)))


class Case:
    def __init__(self, data: dict, p: P):
        self.d, self.p = data, p
        b = data["board"]
        self.bw, self.bt, self.corner = b["w"], b["thickness"], b["cornerR"]
        self.parts = {q["ref"]: q for q in data["parts"]}

        self.cav = self.bw + 2 * p.cavity_pad
        self.ox = self.cav + p.wall_front + p.wall_back   # 深（X）
        self.oy = self.cav + 2 * p.wall_side              # 宽（Y）
        self.cx = (p.wall_back - p.wall_front) / 2        # 外框相对板中心偏移
        self.board_z = p.floor + p.board_gap
        self.board_top = self.board_z + self.bt
        self.lid_top = p.base_h + p.lid_t
        self.front_in = -self.cav / 2                     # 前面板内表面
        self.back_in = self.cav / 2

    # ---------------- 底壳 ----------------
    def base(self) -> cq.Workplane:
        p = self.p
        outer = (cq.Workplane("XY").center(self.cx, 0).rect(self.ox, self.oy)
                 .extrude(p.base_h).edges("|Z").fillet(self.corner + p.wall_side))
        cavity = rprism(self.cav, self.cav, p.base_h - p.floor, self.corner + 0.3, p.floor)
        base = outer.cut(cavity)

        pts = [(bx(q["x"]), by(q["y"])) for q in self.d["board"]["holes"]]
        base = base.union(
            cq.Workplane("XY").workplane(offset=p.floor).pushPoints(pts)
            .circle(p.boss_od / 2).extrude(self.board_z - p.floor)
        ).cut(
            cq.Workplane("XY").workplane(offset=self.board_z + 0.01).pushPoints(pts)
            .circle(p.pilot / 2).extrude(-p.pilot_depth)
        )

        base = (base.union(self.switch_mount()).cut(self.front_open())
                .union(self.panel_ribs()).cut(self.switch_pocket()))
        for cut in self.wall_cuts():
            base = base.cut(cut)
        return base.cut(self.vents())

    def wall_cuts(self) -> list:
        p, c = self.p, self.p.clearance
        top = self.board_top
        cuts = []

        def yz(ref: str):
            x0, y0, x1, y1 = self.parts[ref]["bb"]
            return by(y1), by(y0)  # Y0, Y1

        # 背面：按插头口开，两个口之间的筋 < 1.2mm 就并成一个连接器舱
        rects = []
        for ref, (mw, mh, rr, zoff, pw, ph) in PORT_SHAPE.items():
            y0, y1 = yz(ref)
            yc = (y0 + y1) / 2
            zc = top + zoff
            rects.append([yc - mw / 2, yc + mw / 2, zc - mh / 2, zc + mh / 2, rr])
        merged = True
        while merged:
            merged = False
            for i in range(len(rects)):
                for j in range(i + 1, len(rects)):
                    a, b = rects[i], rects[j]
                    if (a[0] - 1.2 < b[1] and b[0] - 1.2 < a[1]
                            and a[2] - 1.2 < b[3] and b[2] - 1.2 < a[3]):
                        rects[i] = [min(a[0], b[0]), max(a[1], b[1]),
                                    min(a[2], b[2]), max(a[3], b[3]),
                                    min(a[4], b[4])]
                        rects.pop(j)
                        merged = True
                        break
                if merged:
                    break
        for y0, y1, z0, z1, rr in rects:
            opening = box(self.back_in + 1.4, self.ox / 2 + self.cx + 1,
                          y0 - c, y1 + c, z0 - c, z1 + c)
            cuts.append(opening.edges("|X").fillet(rr))
        # 连接器本体比插头口宽，会在壁内咬进 1~1.2mm：内表面挖 1.6mm 让位槽
        for ref, (mw, mh, rr, zoff, pw, ph) in PORT_SHAPE.items():
            y0, y1 = yz(ref)
            zc = top + zoff
            pocket = box(self.back_in - 0.1, self.back_in + 1.6,
                         y0 - c, y1 + c, top - c, top + PART_HEIGHTS[ref] + c)
            cuts.append(pocket.edges("|X").fillet(0.6))

        y0, y1 = yz("J3")  # microSD，前面板
        cuts.append(box(-self.ox / 2 + self.cx - 1, self.front_in + 0.1, y0 - c, y1 + c,
                        5.0, p.panel_z0 + 0.5))

        # D1/D2 观察孔：开在前脸裙边，正对 LED 侧面（与 TF 槽同一高度带）
        for ref in ("D1", "D2"):
            x0, y0, x1, y1 = self.parts[ref]["bb"]
            cy = (by(y0) + by(y1)) / 2
            cz = top + PART_HEIGHTS[ref] / 2
            cuts.append(cyl((-self.ox / 2 + self.cx - 1, cy, cz), p.led_hole / 2,
                            p.wall_front + 2, (1, 0, 0)))

        # 拨动开关滑槽：横跨拨柄的两个极限位置
        slot_w = p.switch_knob_w + p.switch_travel + p.switch_slot_pad
        slot_cx = p.switch_slot_center
        cuts.append(box(slot_cx - slot_w / 2, slot_cx + slot_w / 2,
                        -self.oy / 2 - 1, -self.cav / 2 + 0.1,
                        p.switch_z - p.switch_knob_h / 2 - 0.2,
                        p.switch_z + p.switch_knob_h / 2 + 0.2))
        # −Y 侧面下部：K2 用户键开 Ø4.2（加长杆），K1 MASK 只留 Ø1.8 针孔
        for ref, dia in (("K2", p.button_hole), ("K1", p.mask_hole)):
            x0, y0, x1, y1 = self.parts[ref]["bb"]
            cuts.append(cyl(((bx(x0) + bx(x1)) / 2, -self.oy / 2 - 1, top + 1.65),
                            dia / 2, p.wall_side + 2, (0, 1, 0)))
        return cuts

    def switch_pocket(self) -> cq.Workplane:
        """内壁沉台：让开关本体往墙里沉，拨柄多露 1mm。"""
        p = self.p
        return box(-p.switch_pocket_w / 2, p.switch_pocket_w / 2,
                   -self.cav / 2, -self.cav / 2 - p.switch_sink,
                   p.switch_z - p.switch_pocket_h / 2, p.switch_z + p.switch_pocket_h / 2)

    def switch_mount(self) -> cq.Workplane:
        """开关腔：两条侧筋（X 向微过盈压配）+ 顶盖 + 底托，本体从腔内压进去。"""
        p = self.p
        z = p.switch_z
        x_in = p.switch_body_l / 2 - p.switch_grip      # 侧筋内表面
        zh = p.switch_body_h / 2 + p.switch_play
        y_in = -self.cav / 2 - p.switch_sink            # 沉台底面
        y_back = y_in + p.switch_body_d + 0.4
        g = None
        for sx in (-1, 1):
            x0, x1 = sx * x_in, sx * (x_in + 1.2)
            rib = box(min(x0, x1), max(x0, x1), y_in, y_back, z - zh - 1.0, z + zh + 1.0)
            g = rib if g is None else g.union(rib)
        g = g.union(box(-x_in, x_in, y_in, y_back, z + zh, z + zh + 0.8))       # 顶盖
        g = g.union(box(-x_in, x_in, y_in, y_back, z - zh - 0.8, z - zh))       # 底托
        return g

    def _outer_prism(self, z0: float, h: float) -> cq.Workplane:
        """外壳外轮廓（平面圆角 r5）：面板与前面开口都从它切出来。"""
        return rprism(self.ox, self.oy, h, self.corner + self.p.wall_side, z0).translate((self.cx, 0, 0))

    def front_open(self) -> cq.Workplane:
        """裙边以上的前壁按外轮廓掏掉，切口圆角与外壳一致。"""
        p = self.p
        cutter = box(-self.ox / 2 - 1, self.front_in + 0.1,
                     -self.oy / 2 - 1, self.oy / 2 + 1, p.panel_z0, p.base_h + 1)
        return self._outer_prism(p.panel_z0, p.base_h - p.panel_z0 + 1).intersect(cutter)

    def panel_ribs(self) -> cq.Workplane:
        """两侧滑槽筋：嵌进前面板背面的 1.2×1.2 台阶里。"""
        p = self.p
        g = None
        for sy in (-1, 1):
            y0, y1 = sy * self.cav / 2, sy * (self.cav / 2 + p.panel_rib)
            rib = box(self.front_in - p.panel_rib, self.front_in + 0.1,
                      min(y0, y1), max(y0, y1), p.panel_z0, p.base_h)
            g = rib if g is None else g.union(rib)
        return g

    def front_panel(self) -> cq.Workplane:
        """滑入式前面板：56 × 35.3 × 3.4，视窗 + 4 个 M2 螺柱 + 两侧滑槽台阶。"""
        p = self.p
        x0, x1 = self.front_in - p.wall_front, self.front_in
        h = p.base_h - p.panel_z0
        zc = p.screen_bottom + p.screen_h / 2
        slab = box(x0 - 0.5, x1, -self.oy / 2 - 1, self.oy / 2 + 1, p.panel_z0, p.base_h)
        plate = slab.intersect(self._outer_prism(p.panel_z0, h))
        win = p.screen_active + 0.4
        plate = plate.cut(box(x0 - 1, x1 + 1,
                              p.screen_dy - win / 2, p.screen_dy + win / 2,
                              zc - win / 2, zc + win / 2))
        # 背面两侧台阶（滑槽）
        for sy in (-1, 1):
            y0, y1 = sy * (self.cav / 2 - 0.1), sy * (self.cav / 2 + p.panel_rib + 0.05)
            plate = plate.cut(box(x1 - p.panel_rib - 0.05, x1 + 0.5,
                                  min(y0, y1), max(y0, y1), p.panel_z0, p.base_h))
        # 4 个 M2 螺柱朝腔内 + 底孔
        for sy in (-1, 1):
            for sz in (-1, 1):
                y = p.screen_dy + sy * p.screen_hole_dx
                z = zc + sz * p.screen_hole_dy
                plate = plate.union(cyl((x1, y, z), p.screen_boss / 2,
                                        p.screen_boss_len, (1, 0, 0)))
                depth = p.screen_boss_len + p.screen_pilot_depth
                plate = plate.cut(cyl((x1 + p.screen_boss_len + 0.01 - depth, y, z),
                                      p.screen_pilot / 2, depth, (1, 0, 0)))
        return plate

    def vents(self) -> cq.Workplane:
        return (cq.Workplane("XY").workplane(offset=-0.5)
                .pushPoints([(x, y) for x in (-14, -5, 4, 13) for y in (-14, -5, 4, 13)])
                .circle(1.5).extrude(self.p.floor + 1))

    # ---------------- 屏幕窗口 + 螺柱（前面板内侧） ----------------
    def screen_window(self) -> cq.Workplane:
        p = self.p
        win = p.screen_active + 0.4
        zc = p.screen_bottom + p.screen_h / 2
        return box(-self.ox / 2 + self.cx - 1, self.front_in + 0.1, p.screen_dy - win / 2,
                   p.screen_dy + win / 2, zc - win / 2, zc + win / 2)

    def screen_holes(self) -> list[tuple[float, float]]:
        p = self.p
        zc = p.screen_bottom + p.screen_h / 2
        return [(p.screen_dy + sx * p.screen_hole_dx, zc + sz * p.screen_hole_dy)
                for sx in (-1, 1) for sz in (-1, 1)]

    # ---------------- 盖板 ----------------
    def lid(self) -> cq.Workplane:
        p = self.p
        z0 = p.base_h
        lid = (cq.Workplane("XY").center(self.cx, 0).rect(self.ox, self.oy)
               .extrude(p.lid_t).edges("|Z").fillet(self.corner + p.wall_side)
               .translate((0, 0, z0)))
        ring_w = self.cav + 2 * p.lip_fit
        lip = rprism(ring_w, ring_w, p.lip, self.corner + 0.3, z0 - p.lip).cut(
            rprism(ring_w - 2 * p.lip_wall, ring_w - 2 * p.lip_wall, p.lip + 2,
                   self.corner - 0.2, z0 - p.lip - 1))
        lid = lid.union(lip)
        # 前侧裙边断开，给托板上沿让位（托板由盖板下表面直接压住）
        lid = lid.cut(box(-self.ox / 2 + self.cx - 1, self.front_in + 2.0, -80, 80,
                          z0 - p.lip - 0.2, z0 + 0.01))
        return lid

    # ---------------- 按键加长杆 ----------------
    def button(self) -> cq.Workplane:
        p = self.p
        gap = p.cavity_pad - 0.05
        stem = cyl((0, 0, 0), p.button_hole / 2 - 0.15, gap + p.button_lip)
        cap = cyl((0, 0, -1.2), 3.0, 1.2)
        return cq.Workplane(obj=stem.val().fuse(cap.val()))

    # ---------------- 检查 ----------------
    def board_modules(self) -> list:
        p = self.p
        mods = [box(-24, 24, -24, 24, self.board_z, self.board_top)]
        for ref, q in self.parts.items():
            x0, y0, x1, y1 = q["bb"]
            if q.get("side") == "back":
                z0, z1 = self.board_z - 2.2, self.board_z
            else:
                z0, z1 = self.board_top, self.board_top + PART_HEIGHTS.get(ref, 6.0)
            mods.append(box(bx(x0), bx(x1), by(y1), by(y0), z0, z1))
        return mods

    def facts(self) -> list[tuple[str, float]]:
        p = self.p
        zc = p.screen_bottom + p.screen_h / 2
        return [
            ("屏幕上沿 → 底壳顶(mm)", p.base_h - (p.screen_bottom + p.screen_h)),
            ("屏幕 → 视窗边(x 向,mm)", p.screen_w / 2 - (p.screen_active + 0.4) / 2),
            ("屏幕背面 → 26-pin 排针(mm)", bx(self.parts["CON30"]["bb"][0])
             - (self.front_in + p.screen_boss_len + p.screen_t)),
            ("屏幕下沿 → TF 槽顶(mm)", p.screen_bottom - (self.board_top + 3.2)),
            ("视窗中心 z", zc),
        ]


def j3_slot_y(case: Case) -> float:
    """TF 槽在 CAD Y 上的中心。"""
    x0, y0, x1, y1 = case.parts["J3"]["bb"]
    return (by(y0) + by(y1)) / 2


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--out", default=str(Path(__file__).parent / "out"))
    ap.add_argument("--check", action="store_true")
    ap.add_argument("--param", action="append", default=[], metavar="k=v")
    a = ap.parse_args()

    p = P()
    for kv in a.param:
        k, v = kv.split("=", 1)
        if k not in {f.name for f in fields(P)}:
            sys.exit(f"未知参数 {k}")
        p = replace(p, **{k: float(v)})

    case = Case(load_board(), p)
    if p.base_h < p.screen_bottom + p.screen_h + 0.5:
        sys.exit("base_h 装不下屏幕：加大 base_h 或下移 screen_bottom")
    if p.screen_boss > 2 * min(p.screen_hole_dx, p.screen_hole_dy) - 0.2:
        sys.exit("屏幕螺钉柱会超出 PCB 边缘")

    base = case.base().cut(case.screen_window())
    panel = case.front_panel()
    lid, button = case.lid(), case.button()
    for name, s in (("base", base), ("lid", lid), ("panel", panel), ("button", button)):
        if not s.val().isValid():
            sys.exit(f"{name} 不是有效实体")
        bb = s.val().BoundingBox()
        print(f"{name:7s} {s.val().Volume()/1000:7.1f} cm3  {bb.xlen:5.1f} × {bb.ylen:5.1f} × {bb.zlen:5.1f} mm")

    print(f"整机     {case.ox:.1f}(深) × {case.oy:.1f}(宽) × {case.lid_top:.1f}(高) mm  "
          f"内腔 {case.cav:.1f}  板顶 z={case.board_top:.1f}")

    worst = 0.0
    for mod in case.board_modules():
        for name, part in (("base", base), ("lid", lid)):
            inter = part.intersect(mod)
            vol = inter.val().Volume() if inter.val() else 0.0
            worst = max(worst, vol)
            if vol > 1.0:
                bb = mod.val().BoundingBox()
                print(f"  !! {name} ∩ 元件 {vol:6.2f} mm3 @ x[{bb.xmin:.1f},{bb.xmax:.1f}] "
                      f"y[{bb.ymin:.1f},{bb.ymax:.1f}]")
    print("板件干涉:", "OK" if worst <= 1.0 else f"最大重叠 {worst:.2f} mm3")

    # 开口是否真的切穿（点包含测试，比看渲染可靠）
    zc = p.screen_bottom + p.screen_h / 2
    sx = case.screen_holes()[0][0]
    sz = case.screen_holes()[0][1]
    probes = {
        "视窗中央": (case.front_in - 1.5, 0, zc, False),
        "面板滑入区(应无料)": (case.front_in - 1.7, 0, 40.0, False),
        "TF 槽": (case.front_in - 1.5, j3_slot_y(case), 7.0, False),
        "RJ45": (case.back_in + 1.5, 9.4, 12.0, False),
        "USB-C": (case.back_in + 1.5, -16.5, 8.0, False),
        "USB-A": (case.back_in + 1.5, -2.7, 14.0, False),
        "开关滑槽": (0, -case.oy / 2 + 1, p.switch_z, False),
        "开关腔侧筋": (-(p.switch_body_l / 2 - p.switch_grip + 0.6), -24.0, p.switch_z, True),
        "K2 按键孔": (-4.5, -case.oy / 2 + 1, case.board_top + 1.65, False),
        "K1 针孔": (2.5, -case.oy / 2 + 1, case.board_top + 1.65, False),
        "滑槽筋(应有料)": (case.front_in - 0.6, case.cav / 2 + 0.6, 30.0, True),
        "裙边(应有料)": (case.front_in - 1.7, 0, 3.0, True),
        "LED 孔 D1": (case.front_in - 1.7, -3.5, 7.05, False),
        "LED 孔 D2": (case.front_in - 1.7, -8.5, 7.05, False),
        "板螺柱根部": (-21.1, 21.1, 1.0, True),
    }
    solid = base.val()
    fails = []
    for name, (x, y, z, want) in probes.items():
        got = solid.isInside(cq.Vector(x, y, z), 1e-6)
        if got != want:
            fails.append(f"{name}(期望{'有料' if want else '无料'})")
    print("开口自检:", "OK" if not fails else "FAIL " + ", ".join(fails))
    for k, v in case.facts():
        print(f"  {k}: {v:+.2f}")

    if a.check:
        return

    out = Path(a.out)
    out.mkdir(parents=True, exist_ok=True)
    for name, s in (("base", base), ("lid", lid), ("panel", panel), ("button", button)):
        exporters.export(s, str(out / f"{name}.stl"), tolerance=0.03, angularTolerance=0.2)
    asm = cq.Assembly()
    asm.add(base, name="base")
    asm.add(lid, name="lid", loc=cq.Location(cq.Vector(0, 0, 6)))
    asm.add(panel, name="panel")
    asm.export(str(out / "assembly.step"))
    print("写出:", out)


if __name__ == "__main__":
    main()
