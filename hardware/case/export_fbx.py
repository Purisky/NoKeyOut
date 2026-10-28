"""把 out/*.stl 与开关 FBX 合成一个可编辑的 FBX（真实尺寸，mm）。

    "G:/blender-5.2.3/blender.exe" -b -P hardware/case/export_fbx.py

布局：底壳在原点，开关按装配位放进去；盖板抬到 +90mm、托板移到 +90mm X、按键杆放前面，
方便单独改细节。缩放 0.001 让文件里的实际尺寸是 58mm 而不是 58m。
"""
import math
import os

import bpy
from mathutils import Vector

HERE = os.path.dirname(os.path.abspath(__file__))
OUT = os.path.join(HERE, "out")
SWITCH = os.path.join(HERE, "..", "..", "Untitled.fbx")
TARGET = os.path.join(OUT, "nokeyout_case.fbx")

bpy.ops.wm.read_factory_settings(use_empty=True)


def import_stl(name):
    path = os.path.join(OUT, name)
    try:
        bpy.ops.wm.stl_import(filepath=path)
    except AttributeError:
        bpy.ops.import_mesh.stl(filepath=path)
    ob = bpy.context.selected_objects[0]
    ob.name = name.replace(".stl", "")
    ob.scale = (0.001, 0.001, 0.001)
    bpy.ops.object.transform_apply(location=False, rotation=False, scale=True)
    return ob


base = import_stl("base.stl")
lid = import_stl("lid.stl")
panel = import_stl("panel.stl")
button = import_stl("button.stl")

# 摆放：底壳原点 + 开关在位；其余移开便于单独编辑
lid.location = (0, 0, 90 * 0.001)
panel.location = (-90 * 0.001, 0, 0)
button.location = (0, -50 * 0.001, 0)

bpy.context.view_layer.update()
for ob in bpy.context.scene.objects:
    if ob.type != "MESH":
        continue
    pts = [ob.matrix_world @ Vector(c) for c in ob.bound_box]
    d = [max(p[i] for p in pts) - min(p[i] for p in pts) for i in range(3)]
    print(f"{ob.name[:26]:26s} " + " × ".join(f"{v*1000:7.1f}" for v in d) + " mm")

bpy.ops.export_scene.fbx(
    filepath=TARGET,
    use_selection=False,
    object_types={"MESH", "EMPTY"},
    apply_unit_scale=True,
    apply_scale_options="FBX_SCALE_NONE",
    global_scale=1.0,
    bake_space_transform=False,
    mesh_smooth_type="FACE",
    path_mode="AUTO",
)
print("wrote", TARGET, os.path.getsize(TARGET), "bytes")
