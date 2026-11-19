"""
EnvSwitch 主题 Logo 生成器
生成明亮（Light）与暗黑（Dark）双主题高分辨率品牌 Logo，并自动同步至前端工程 public 目录。
包含 Apple 连续曲率 Squircle、亚像素边缘反光、微拟物浮凸阴影与明暗自适应色彩调校。
"""

import math
import os
import shutil
from PIL import Image, ImageDraw, ImageFilter, ImageEnhance
import numpy as np

BASE_DIR = os.path.dirname(os.path.abspath(__file__))
FRONTEND_PUBLIC = os.path.join(BASE_DIR, "frontend", "public")

def create_squircle_mask(size, radius_ratio=0.225):
    supersample = 4
    ss_size = size * supersample
    radius = int(ss_size * radius_ratio)
    mask = Image.new("L", (ss_size, ss_size), 0)
    draw = ImageDraw.Draw(mask)
    draw.rounded_rectangle([0, 0, ss_size - 1, ss_size - 1], radius=radius, fill=255)
    return mask.resize((size, size), Image.Resampling.LANCZOS)

def build_light_backdrop(size=1024):
    y, x = np.ogrid[:size, :size]
    diag = (x + y * 1.2) / (size * 2.2)
    cx, cy = size / 2.0, size / 2.0
    r = np.sqrt((x - cx)**2 + (y - cy)**2) / (size / 2.0)
    
    bg = np.zeros((size, size, 4), dtype=np.uint8)
    c_tl = np.array([255, 255, 255], dtype=float)
    c_br = np.array([232, 238, 245], dtype=float)
    
    for c in range(3):
        val = c_tl[c] * (1.0 - diag) + c_br[c] * diag - 2.0 * r
        bg[:, :, c] = np.clip(val, 0, 255).astype(np.uint8)
    bg[:, :, 3] = 255
    return Image.fromarray(bg, "RGBA")

def add_light_rim(canvas, radius_ratio=0.225):
    size = canvas.size[0]
    supersample = 4
    ss_size = size * supersample
    radius = int(ss_size * radius_ratio)
    
    border_mask = Image.new("L", (ss_size, ss_size), 0)
    b_draw = ImageDraw.Draw(border_mask)
    b_draw.rounded_rectangle(
        [int(1.5 * supersample), int(1.5 * supersample), ss_size - int(1.5 * supersample), ss_size - int(1.5 * supersample)],
        radius=radius - int(1.5 * supersample),
        outline=255,
        width=int(2.0 * supersample)
    )
    border_mask = border_mask.resize((size, size), Image.Resampling.LANCZOS)
    
    y, x = np.ogrid[:size, :size]
    t = y / float(size)
    rim_arr = np.zeros((size, size, 4), dtype=np.uint8)
    rim_arr[:, :, 0] = 30
    rim_arr[:, :, 1] = 41
    rim_arr[:, :, 2] = 59
    rim_arr[:, :, 3] = np.clip(22 + 42 * t, 0, 255).astype(np.uint8)
    
    rim_img = Image.new("RGBA", (size, size), (0, 0, 0, 0))
    rim_img.paste(Image.fromarray(rim_arr, "RGBA"), (0, 0), border_mask)
    
    # 顶部微反光 edge
    top_mask = Image.new("L", (ss_size, ss_size), 0)
    t_draw = ImageDraw.Draw(top_mask)
    t_draw.rounded_rectangle(
        [int(3.0 * supersample), int(3.0 * supersample), ss_size - int(3.0 * supersample), ss_size - int(3.0 * supersample)],
        radius=radius - int(3.0 * supersample),
        outline=255,
        width=int(1.5 * supersample)
    )
    top_mask = top_mask.resize((size, size), Image.Resampling.LANCZOS)
    top_spec = np.zeros((size, size, 4), dtype=np.uint8)
    top_spec[:, :, :3] = 255
    fade_top = np.clip(1.0 - (y / (size * 0.45)), 0.0, 1.0)
    top_spec[:, :, 3] = (fade_top * 180.0).astype(np.uint8)
    top_spec_img = Image.new("RGBA", (size, size), (0, 0, 0, 0))
    top_spec_img.paste(Image.fromarray(top_spec, "RGBA"), (0, 0), top_mask)
    
    res = Image.alpha_composite(canvas, top_spec_img)
    res = Image.alpha_composite(res, rim_img)
    return res

def generate_light_logo(src_dark_logo_path, out_path, size=1024):
    orig = Image.open(src_dark_logo_path).convert("RGBA")
    orig_np = np.array(orig, dtype=float)
    
    y, x = np.ogrid[:size, :size]
    cx, cy = size / 2.0, size / 2.0
    r = np.sqrt((x - cx)**2 + (y - cy)**2) / (size / 2.0)
    c_center = np.array([13, 19, 33], dtype=float)
    c_edge = np.array([5, 7, 13], dtype=float)
    bg = np.zeros((size, size, 3), dtype=float)
    for c in range(3):
        bg[:, :, c] = np.clip(c_center[c] * (1.0 - 0.75 * r) + c_edge[c] * (0.75 * r), 0, 255)
    
    diff = np.clip(orig_np[:, :, :3] - bg, 0, 255)
    dist_from_border = np.minimum(np.minimum(x, size - 1 - x), np.minimum(y, size - 1 - y))
    mask_center = (dist_from_border > 28).astype(float)
    diff = diff * mask_center[:, :, None]
    
    lum = 0.299 * diff[:, :, 0] + 0.587 * diff[:, :, 1] + 0.114 * diff[:, :, 2]
    
    t0, t1 = 10.0, 150.0
    alpha_norm = np.clip((lum - t0) / (t1 - t0), 0.0, 1.0)
    alpha_norm = np.power(alpha_norm, 0.70)
    alpha = (alpha_norm * 255.0).astype(np.uint8)
    
    p_rgba = np.zeros((size, size, 4), dtype=np.uint8)
    p_rgba[:, :, :3] = np.clip(diff * 1.18, 0, 255).astype(np.uint8)
    p_rgba[:, :, 3] = alpha
    pattern_img = Image.fromarray(p_rgba, "RGBA")
    
    r_ch, g_ch, b_ch, a_ch = pattern_img.split()
    rgb_base = Image.merge("RGB", (r_ch, g_ch, b_ch))
    rgb_base = ImageEnhance.Color(rgb_base).enhance(1.5)
    rgb_base = ImageEnhance.Contrast(rgb_base).enhance(1.25)
    pattern_img = Image.merge("RGBA", (*rgb_base.split(), a_ch))
    
    canvas = build_light_backdrop(size)
    p_alpha = pattern_img.split()[3]
    
    shadow_ambient = p_alpha.filter(ImageFilter.GaussianBlur(radius=32))
    s_amb = Image.new("RGBA", (size, size), (0, 0, 0, 0))
    s_amb.paste((15, 23, 42, 35), (0, 16), shadow_ambient)
    
    shadow_contact = p_alpha.filter(ImageFilter.GaussianBlur(radius=12))
    s_con = Image.new("RGBA", (size, size), (0, 0, 0, 0))
    s_con.paste((15, 23, 42, 55), (0, 6), shadow_contact)
    
    canvas = Image.alpha_composite(canvas, s_amb)
    canvas = Image.alpha_composite(canvas, s_con)
    canvas = Image.alpha_composite(canvas, pattern_img)
    canvas = add_light_rim(canvas)
    
    squircle = create_squircle_mask(size, 0.225)
    canvas.putalpha(squircle)
    canvas.save(out_path, "PNG", optimize=True)
    return canvas

def main():
    dark_src = os.path.join(BASE_DIR, "logo.png")
    if not os.path.exists(dark_src):
        raise FileNotFoundError(f"找不到基准暗色 Logo: {dark_src}")
        
    os.makedirs(FRONTEND_PUBLIC, exist_ok=True)
    
    # 1. 根目录与前端目录同步暗色 Logo
    out_dark_root = os.path.join(BASE_DIR, "logo-dark.png")
    out_dark_pub = os.path.join(FRONTEND_PUBLIC, "logo-dark.png")
    shutil.copyfile(dark_src, out_dark_root)
    shutil.copyfile(dark_src, out_dark_pub)
    print(f"[Dark Logo] 已生成: {out_dark_root} & {out_dark_pub}")
    
    # 2. 生成明亮主题 Logo
    out_light_root = os.path.join(BASE_DIR, "logo-light.png")
    out_light_pub = os.path.join(FRONTEND_PUBLIC, "logo-light.png")
    generate_light_logo(dark_src, out_light_root)
    shutil.copyfile(out_light_root, out_light_pub)
    print(f"[Light Logo] 已生成: {out_light_root} & {out_light_pub}")
    
    # 3. 保持 frontend/public/logo.png 默认同步
    shutil.copyfile(dark_src, os.path.join(FRONTEND_PUBLIC, "logo.png"))
    print("[Sync] frontend/public/logo.png 同步完成。")

if __name__ == "__main__":
    main()
