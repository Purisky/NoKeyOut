"""把 out/*.stl 打包成 Blender 可直接打开的 .blend / .glb，并渲染两张图。

    "G:/blender-5.2.3/blender.exe" -b -P hardware/case/blender_export.py

单位：1 Blender unit = 1 mm（与 STL 一致，不做缩放）。
"""
import math
import os

import bpy
from mathutils import Vector

HERE = os.path.dirname(os.path.abspath(__file__))
OUT = os.path.join(HERE, "out")
PARTS = [("open_frame", "open_frame.stl", (0.80, 0.82, 0.85, 1)),
         ("open_frame_dark", None, None)]
TARGET = os.path.join(OUT, "open_frame.blend")

bpy.ops.wm.read_factory_settings(use_empty=True)


def import_stl(name):
    path = os.path.join(OUT, name)
    try:
        bpy.ops.wm.stl_import(filepath=path)
    except AttributeError:
        bpy.ops.import_mesh.stl(filepath=path)
    ob = bpy.context.selected_objects[0]
    ob.name = name.replace(".stl", "")
    return ob


frame = import_stl("open_frame.stl")
frame.color = (0.80, 0.82, 0.85, 1)
frame.data.materials.append(bpy.data.materials.new("frame"))

bpy.ops.mesh.primitive_plane_add(size=900, location=(0, 0, -0.3))
ground = bpy.context.object
ground.name = "ground"
ground.color = (0.17, 0.18, 0.20, 1)
ground.data.materials.append(bpy.data.materials.new("ground"))

# 保存 .blend（相机、灯光、材质都在里面）
bpy.ops.wm.save_as_mainfile(filepath=TARGET)
print("wrote", TARGET)

# 导出 .glb（Stl 已在场景中，单位 mm）
bpy.ops.object.select_all(action="DESELECT")
frame.select_set(True)
bpy.context.view_layer.objects.active = frame
bpy.ops.export_scene.gltf(filepath=os.path.join(OUT, "open_frame.glb"),
                          export_format="GLB", use_selection=True)
print("wrote open_frame.glb")


def setup_render():
    sc = bpy.context.scene
    try:
        sc.render.engine = "BLENDER_WORKBENCH"
    except TypeError:
        raise SystemExit([i.identifier for i in
                          bpy.types.RenderSettings.bl_rna.properties["engine"].enum_items])
    sh = sc.display.shading
    sh.light = "STUDIO"
    sh.show_shadows = True
    sh.shadow_intensity = 0.30
    sh.show_cavity = True
    sh.cavity_type = "WORLD"
    sh.color_type = "OBJECT"
    sh.background_type = "VIEWPORT"
    sh.background_color = (0.12, 0.13, 0.15)
    cam = bpy.data.objects.new("cam", bpy.data.cameras.new("cam"))
    bpy.context.collection.objects.link(cam)
    cam.data.type = "ORTHO"
    sc.camera = cam
    sc.render.resolution_x = 1400
    sc.render.resolution_y = 900
    sc.render.image_settings.file_format = "PNG"
    return sc, cam


def shot(sc, cam, name, eye, tgt, scale):
    cam.location = Vector(eye)
    cam.rotation_euler = (cam.location - Vector(tgt)).to_track_quat("Z", "Y").to_euler()
    cam.data.ortho_scale = scale
    sc.render.filepath = os.path.join(OUT, name)
    bpy.ops.render.render(write_still=True)
    print("wrote", name)


sc, cam = setup_render()
shot(sc, cam, "frame-front.png", (-150, -170, 130), (0, 0, 19), 120)
shot(sc, cam, "frame-back.png", (170, 150, 120), (0, 0, 19), 120)
