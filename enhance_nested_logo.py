import math
from PIL import Image, ImageDraw, ImageFilter, ImageEnhance
import numpy as np
import os

def create_squircle_mask(size, radius_ratio=0.225):
    """
    Creates an ultra-smooth antialiased squircle / rounded rectangle mask
    with 4x supersampling.
    """
    supersample = 4
    ss_size = size * supersample
    radius = int(ss_size * radius_ratio)
    
    mask = Image.new("L", (ss_size, ss_size), 0)
    draw = ImageDraw.Draw(mask)
    draw.rounded_rectangle([0, 0, ss_size - 1, ss_size - 1], radius=radius, fill=255)
    return mask.resize((size, size), Image.Resampling.LANCZOS)

def build_high_tech_backdrop(size=1024):
    """
    Creates an ultra-futuristic deep slate obsidian backdrop with subtle
    radial luminescence and micro-grid tech depth.
    """
    y, x = np.ogrid[:size, :size]
    cx, cy = size / 2.0, size / 2.0
    r = np.sqrt((x - cx)**2 + (y - cy)**2) / (size / 2.0)
    
    # Deep midnight slate to pure carbon obsidian
    c_center = np.array([13, 19, 33], dtype=float)  # #0D1321
    c_edge = np.array([5, 7, 13], dtype=float)       # #05070D
    
    bg = np.zeros((size, size, 4), dtype=np.uint8)
    for c in range(3):
        bg[:, :, c] = np.clip(c_center[c] * (1.0 - 0.75 * r) + c_edge[c] * (0.75 * r), 0, 255).astype(np.uint8)
    bg[:, :, 3] = 255
    
    base_img = Image.fromarray(bg, "RGBA")
    return base_img

def enhance_and_enlarge_nested_logo():
    base_dir = r"e:\idea work\EnvSwitch"
    src = r"C:\Users\HUIDADA\.gemini\antigravity\brain\0f6af744-2722-4a3d-8dfd-852967ca1f3a\envswitch_logo_icon_1794988052825.jpg"
    
    orig = Image.open(src).convert("RGB")
    # Base squircle tile inside original was at (145, 145, 878, 878)
    tile = orig.crop((145, 145, 878, 878))
    tile_np = np.array(tile, dtype=float)
    
    # The actual pattern is within x: [60, 670], y: [120, 610]
    pattern_crop = tile.crop((55, 125, 675, 605))
    p_np = np.array(pattern_crop, dtype=float)
    
    # The background of the tile is around [27, 27, 27] to [35, 35, 35]
    # We estimate local dark background and extract only luminous details
    bg_est = np.percentile(p_np, 15, axis=(0, 1))
    
    # Difference from background
    diff = np.clip(p_np - bg_est, 0, 255)
    
    # Calculate luminance of the diff
    lum = 0.299 * diff[:, :, 0] + 0.587 * diff[:, :, 1] + 0.114 * diff[:, :, 2]
    
    # Smooth threshold curve: anything with lum > 10 starts appearing, ramps smoothly
    # to eliminate all background noise while keeping delicate neon edges
    t0, t1 = 8.0, 75.0
    alpha_norm = np.clip((lum - t0) / (t1 - t0), 0.0, 1.0)
    # Gamma curve for crisp edge + soft falloff
    alpha_norm = np.power(alpha_norm, 0.85)
    alpha = (alpha_norm * 255.0).astype(np.uint8)
    
    # Boost RGB colors: enhance saturation and vibrancy
    rgb = np.clip(diff * 1.35, 0, 255).astype(np.uint8)
    
    # Assemble RGBA extracted graphic
    extracted_rgba = np.zeros((p_np.shape[0], p_np.shape[1], 4), dtype=np.uint8)
    extracted_rgba[:, :, :3] = rgb
    extracted_rgba[:, :, 3] = alpha
    extracted_img = Image.fromarray(extracted_rgba, "RGBA")
    
    # Enhance extracted pattern colors (vibrant neon tech look)
    r, g, b, a = extracted_img.split()
    rgb_img = Image.merge("RGB", (r, g, b))
    rgb_img = ImageEnhance.Color(rgb_img).enhance(1.4)
    rgb_img = ImageEnhance.Contrast(rgb_img).enhance(1.3)
    r_n, g_n, b_n = rgb_img.split()
    extracted_img = Image.merge("RGBA", (r_n, g_n, b_n, a))
    
    # Now enlarge the graphic:
    # Target size: 1024x1024 canvas
    # Make pattern width ~ 830px (fills ~81% of the width! Bold, prominent, high impact!)
    target_canvas_size = 1024
    target_w = 830
    target_h = int(extracted_img.height * (target_w / extracted_img.width))
    
    enlarged_pattern = extracted_img.resize((target_w, target_h), Image.Resampling.LANCZOS)
    
    # Center coordinates
    paste_x = (target_canvas_size - target_w) // 2
    paste_y = (target_canvas_size - target_h) // 2
    
    # Create final tech canvas
    canvas = build_high_tech_backdrop(target_canvas_size)
    
    # 1. Neon atmospheric glow layers (cyan / emerald / violet)
    p_alpha = enlarged_pattern.split()[3]
    
    # Wide ambient bloom
    glow_wide = p_alpha.filter(ImageFilter.GaussianBlur(radius=42))
    glow_layer1 = Image.new("RGBA", (target_canvas_size, target_canvas_size), (0, 0, 0, 0))
    glow_layer1.paste((6, 182, 212, 55), (paste_x, paste_y), glow_wide)  # Cyan glow
    
    # Mid-range bloom
    glow_mid = p_alpha.filter(ImageFilter.GaussianBlur(radius=20))
    glow_layer2 = Image.new("RGBA", (target_canvas_size, target_canvas_size), (0, 0, 0, 0))
    glow_layer2.paste((139, 92, 246, 75), (paste_x, paste_y), glow_mid) # Violet glow
    
    # Inner tight bloom
    glow_tight = p_alpha.filter(ImageFilter.GaussianBlur(radius=8))
    glow_layer3 = Image.new("RGBA", (target_canvas_size, target_canvas_size), (0, 0, 0, 0))
    glow_layer3.paste((16, 185, 129, 65), (paste_x, paste_y), glow_tight) # Emerald glow
    
    canvas = Image.alpha_composite(canvas, glow_layer1)
    canvas = Image.alpha_composite(canvas, glow_layer2)
    canvas = Image.alpha_composite(canvas, glow_layer3)
    
    # 2. Main sharp prominent pattern layer
    p_layer = Image.new("RGBA", (target_canvas_size, target_canvas_size), (0, 0, 0, 0))
    p_layer.paste(enlarged_pattern, (paste_x, paste_y), enlarged_pattern)
    canvas = Image.alpha_composite(canvas, p_layer)
    
    # 3. Add refined luxury border highlight (Apple macOS app icon precision rim)
    radius_ratio = 0.225
    supersample = 4
    ss_size = target_canvas_size * supersample
    radius = int(ss_size * radius_ratio)
    
    border_mask = Image.new("L", (ss_size, ss_size), 0)
    b_draw = ImageDraw.Draw(border_mask)
    b_draw.rounded_rectangle(
        [int(2 * supersample), int(2 * supersample), ss_size - int(2 * supersample), ss_size - int(2 * supersample)],
        radius=radius - int(2 * supersample),
        outline=255,
        width=int(2.2 * supersample)
    )
    border_mask = border_mask.resize((target_canvas_size, target_canvas_size), Image.Resampling.LANCZOS)
    
    # Linear fade highlight from top-left to bottom-right
    y, x = np.ogrid[:target_canvas_size, :target_canvas_size]
    t = 1.0 - ((x + y) / (2.0 * target_canvas_size))
    rim_arr = np.zeros((target_canvas_size, target_canvas_size, 4), dtype=np.uint8)
    rim_arr[:, :, 0] = 255
    rim_arr[:, :, 1] = 255
    rim_arr[:, :, 2] = 255
    rim_arr[:, :, 3] = np.clip(15 + 65 * t, 0, 255).astype(np.uint8)
    
    rim_img = Image.new("RGBA", (target_canvas_size, target_canvas_size), (0, 0, 0, 0))
    rim_img.paste(Image.fromarray(rim_arr, "RGBA"), (0, 0), border_mask)
    canvas = Image.alpha_composite(canvas, rim_img)
    
    # 4. Final Squircle Crop (Pure 1:1 with Transparent Rounded Corners)
    squircle = create_squircle_mask(target_canvas_size, radius_ratio)
    canvas.putalpha(squircle)
    
    # Save outputs
    out_logo = os.path.join(base_dir, "logo.png")
    out_alt = os.path.join(base_dir, "logo_alt_nested.png")
    canvas.save(out_logo, "PNG", optimize=True)
    canvas.save(out_alt, "PNG", optimize=True)
    print(f"Saved primary logo.png ({target_canvas_size}x{target_canvas_size}, pattern width={target_w}px)")
    print(f"Saved logo_alt_nested.png ({target_canvas_size}x{target_canvas_size})")
    
    # Update multi-resolution icons
    sizes = [512, 256, 128, 64, 32]
    for s in sizes:
        resized = canvas.resize((s, s), Image.Resampling.LANCZOS)
        out_p = os.path.join(base_dir, f"logo_{s}.png")
        resized.save(out_p, "PNG", optimize=True)
        print(f"Updated: {out_p}")
        
    # Update favicon.ico
    ico_sizes = [(256, 256), (128, 128), (64, 64), (48, 48), (32, 32), (16, 16)]
    ico_path = os.path.join(base_dir, "favicon.ico")
    canvas.save(ico_path, format="ICO", sizes=ico_sizes)
    print(f"Updated: {ico_path}")

if __name__ == "__main__":
    enhance_and_enlarge_nested_logo()
