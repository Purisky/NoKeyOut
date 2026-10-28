"""NoKeyOut 开放式框架（六角铜柱版）

结构：一块 L 形打印件
  * 底板上四个 Ø3.4 孔 → M3 铜柱（母母 10mm）→ 主板
  * 前立板带 28.6 视窗 + 4 个 M2 自攻螺柱 → 屏幕（44×32）
  * 前立板左侧开滑槽 + 压配腔 → 拨动开关（12×6×6，行程 2.5）

坐标：主板中心在原点，X 为前后（−X 前），Y 为左右，Z 向上。
用法：python open_frame.py [--out out]
"""

from __future__ import annotations

import argparse
import sys
from pathlib import Path

import cadquery as cq
from cadquery import exporters

# ---- 尺寸（mm） ----
PLATE_T = 3.0          # 底板厚
PLATE_W = 58.0         # 沿 X
PLATE_D = 68.0         # 沿 Y（左侧多出 12mm 给开关）
PLATE_CY = -6.0        # 底板中心在 Y 上偏移（开关在 −Y 侧）
CORNER = 6.0

UPRIGHT_T = 3.4        # 前立板厚
UPRIGHT_H = 35.0       # 前立板高（从底板顶面算）
UPRIGHT_X0 = -31.0     # 前立板外表面 x（再往前 2mm，让屏板完全在主板前缘之外）

STANDOFF_D = 3.4       # M3 通孔
STANDOFF_PITCH = 42.2  # 42.2 = 45.1 − 2.9

SCREEN_W, SCREEN_H = 44.0, 32.0
SCREEN_Z0 = 5.0        # 屏幕下沿 z
SCREEN_ACTIVE = 28.2
SCREEN_HOLE_DX, SCREEN_HOLE_DY = 19.5, 13.5
BOSS_D, BOSS_L, PILOT_D, PILOT_L = 4.4, 0.8, 1.6, 1.8

SW_L, SW_D, SW_H = 12.0, 6.0, 6.0     # 本体
SW_KNOB_W, SW_KNOB_H = 4.0, 3.0       # 拨柄
SW_TRAVEL = 2.5
SW_GRIP = 0.1                         # 单边过盈
SW_CY = -31.0                         # 开关中心 y
SW_CZ = 9.0                           # 开关中心 z


def rprism(w: float, d: float, h: float, r: float, z0: float = 0.0) -> cq.Workplane:
    return cq.Workplane("XY").workplane(offset=z0).rect(w, d).extrude(h).edges("|Z").fillet(r)


def box(x0, x1, y0, y1, z0, z1) -> cq.Workplane:
    return (cq.Workplane("XY").box(abs(x1 - x0), abs(y1 - y0), abs(z1 - z0))
            .translate(((x0 + x1) / 2, (y0 + y1) / 2, (z0 + z1) / 2)))


def cyl(center, r: float, h: float, vec=(0, 0, 1)) -> cq.Workplane:
    return cq.Workplane(obj=cq.Solid.makeCylinder(r, h, cq.Vector(*center), cq.Vector(*vec)))


def build() -> cq.Workplane:
    z_plate = PLATE_T
    z_top = z_plate + UPRIGHT_H
    y0, y1 = PLATE_CY - PLATE_D / 2, PLATE_CY + PLATE_D / 2
    plate = (rprism(PLATE_W, PLATE_D, PLATE_T, CORNER)
             .translate((UPRIGHT_X0 + PLATE_W / 2, PLATE_CY, 0)))
    upright = box(UPRIGHT_X0, UPRIGHT_X0 + UPRIGHT_T, y0, y1, z_plate, z_top)
    frame = plate.union(upright)

    # 铜柱孔（Ø3.4）
    half = STANDOFF_PITCH / 2
    for sx in (-1, 1):
        for sy in (-1, 1):
            frame = frame.cut(cyl((sx * half, sy * half, -0.5), STANDOFF_D / 2, PLATE_T + 1))

    # 视窗
    zc = SCREEN_Z0 + SCREEN_H / 2
    win = SCREEN_ACTIVE + 0.4
    frame = frame.cut(box(UPRIGHT_X0 - 1, UPRIGHT_X0 + UPRIGHT_T + 1,
                          -win / 2, win / 2, zc - win / 2, zc + win / 2))

    # 屏幕螺柱 + 底孔（朝 +X，即腔内）
    xb = UPRIGHT_X0 + UPRIGHT_T
    for sy in (-1, 1):
        for sz in (-1, 1):
            y = sy * SCREEN_HOLE_DX
            z = zc + sz * SCREEN_HOLE_DY
            frame = frame.union(cyl((xb, y, z), BOSS_D / 2, BOSS_L, (1, 0, 0)))
            frame = frame.cut(cyl((xb + BOSS_L + 0.01 - (BOSS_L + PILOT_L), y, z),
                                  PILOT_D / 2, BOSS_L + PILOT_L, (1, 0, 0)))

    # 开关滑槽（穿透立板）
    slot_w = SW_KNOB_W + SW_TRAVEL
    frame = frame.cut(box(UPRIGHT_X0 - 1, xb + 0.1,
                          SW_CY - slot_w / 2, SW_CY + slot_w / 2,
                          SW_CZ - SW_KNOB_H / 2 - 0.2, SW_CZ + SW_KNOB_H / 2 + 0.2))

    # 开关压配腔：两条侧筋 + 顶盖 + 底托（本体从 +X 压进来）
    x_in = xb
    x_back = x_in + SW_D + 0.4
    yh = SW_L / 2 - SW_GRIP
    zh = SW_H / 2 + 0.2
    for sy in (-1, 1):
        ya, yb = SW_CY + sy * yh, SW_CY + sy * (yh + 1.2)
        frame = frame.union(box(x_in, x_back, min(ya, yb), max(ya, yb),
                                SW_CZ - zh - 1.0, SW_CZ + zh + 1.0))
    frame = frame.union(box(x_in, x_back, SW_CY - yh, SW_CY + yh,
                            SW_CZ + zh, SW_CZ + zh + 0.8))
    frame = frame.union(box(x_in, x_back, SW_CY - yh, SW_CY + yh,
                            SW_CZ - zh - 0.8, SW_CZ - zh))
    return frame


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--out", default=str(Path(__file__).parent / "out"))
    a = ap.parse_args()

    s = build()
    if not s.val().isValid():
        sys.exit("实体无效")
    bb = s.val().BoundingBox()
    print(f"frame {s.val().Volume()/1000:.1f} cm³  "
          f"{bb.xlen:.1f} × {bb.ylen:.1f} × {bb.zlen:.1f} mm")

    # 自检
    v = s.val()
    probes = {
        "铜柱孔(应空)": (STANDOFF_PITCH / 2, STANDOFF_PITCH / 2, 1.5, False),
        "视窗(应空)": (UPRIGHT_X0 + 1.7, 0, SCREEN_Z0 + SCREEN_H / 2, False),
        "视窗上框(应有料)": (UPRIGHT_X0 + 1.7, 0, UPRIGHT_H + PLATE_T - 1.5, True),
        "屏幕螺柱(应有料)": (UPRIGHT_X0 + UPRIGHT_T + 0.4, 19.5 + 1.5, 34.5, True),
        "开关滑槽(应空)": (UPRIGHT_X0 + 1.7, SW_CY, SW_CZ, False),
        "开关腔侧筋(应有料)": (UPRIGHT_X0 + UPRIGHT_T + 0.5, SW_CY + SW_L / 2 + 0.2, SW_CZ, True),
        "底板(应有料)": (-10, 10, 1.5, True),
    }
    fails = [n for n, (x, y, z, want) in probes.items()
             if v.isInside(cq.Vector(x, y, z), 1e-6) != want]
    print("自检:", "OK" if not fails else "FAIL " + ", ".join(fails))

    out = Path(a.out)
    out.mkdir(parents=True, exist_ok=True)
    exporters.export(s, str(out / "open_frame.stl"), tolerance=0.03, angularTolerance=0.2)
    exporters.export(s, str(out / "open_frame.step"))
    print("写出:", out / "open_frame.stl")


if __name__ == "__main__":
    main()
