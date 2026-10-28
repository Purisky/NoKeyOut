"""Blender 无头渲染两张图：装配/爆炸视图 + 底壳端口视图。

    blender -b -P render.py
"""
import math
import os

import bpy
from mathutils import Vector

HERE = os.path.dirname(os.path.abspath(__file__))
OUT = os.path.join(HERE, "out")

bpy.ops.wm.read_factory_settings(use_empty=True)


def import_stl(name):
    path = os.path.join(OUT, name)
    try:
        bpy.ops.wm.stl_import(filepath=path)
    except AttributeError:
        bpy.ops.import_mesh.stl(filepath=path)
    return bpy.context.selected_objects[0]


def paint(ob, rgba):
    ob.color = rgba
    ob.data.materials.append(bpy.data.materials.new(ob.name))


base = import_stl("base.stl")
lid = import_stl("lid.stl")
panel = import_stl("panel.stl")
button = import_stl("button.stl")
paint(base, (0.80, 0.82, 0.85, 1))
paint(panel, (0.72, 0.66, 0.55, 1))
paint(lid, (0.60, 0.65, 0.72, 1))
paint(button, (0.95, 0.62, 0.18, 1))

# 盖板翻过来悬在上方 70mm：露出摩擦裙边
lid.rotation_euler.x = math.pi
lid.location.z = 45.5 + 70

# 前面板立在托盘右侧，屏幕面朝外
panel.rotation_euler.y = math.radians(-90)
panel.location = (55, 12, 0)

# 按键放在托盘前侧
button.rotation_euler.x = math.radians(90)
button.location = (40, -34, 3)

bpy.ops.mesh.primitive_plane_add(size=1200, location=(0, 0, -0.3))
paint(bpy.context.object, (0.17, 0.18, 0.20, 1))

scene = bpy.context.scene
try:
    scene.render.engine = "BLENDER_WORKBENCH"
except TypeError:
    raise SystemExit([i.identifier for i in
                      bpy.types.RenderSettings.bl_rna.properties["engine"].enum_items])

sh = scene.display.shading
sh.light = "STUDIO"
sh.show_shadows = True
sh.shadow_intensity = 0.30
sh.show_cavity = True
sh.cavity_type = "WORLD"
sh.color_type = "OBJECT"
sh.background_type = "VIEWPORT"
sh.background_color = (0.11, 0.12, 0.14)

cam_data = bpy.data.cameras.new("cam")
cam_data.type = "ORTHO"
cam = bpy.data.objects.new("cam", cam_data)
bpy.context.collection.objects.link(cam)
scene.camera = cam

scene.render.resolution_x = 1600
scene.render.resolution_y = 1000
scene.render.image_settings.file_format = "PNG"


def shot(path, eye, target, scale):
    cam.location = Vector(eye)
    cam.rotation_euler = (cam.location - Vector(target)).to_track_quat("Z", "Y").to_euler()
    cam_data.ortho_scale = scale
    scene.render.filepath = os.path.join(OUT, path)
    bpy.ops.render.render(write_still=True)
    print("wrote", scene.render.filepath)


shot("preview.png", (-330, -300, 300), (0, 0, 24), 300)

# 背面视图：只留底壳，从 +X 后上方看三个端口开口
lid.hide_render = True
button.hide_render = True
shot("preview-ports.png", (330, 230, 200), (0, 0, 16), 230)
