import math
from PIL import Image, ImageDraw, ImageFilter, ImageOps
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
    
    # Downsample to target size with Box or Lanczos
    return mask.resize((size, size), Image.Resampling.LANCZOS)

def add_subtle_inner_border(img_rgba, radius_ratio=0.225):
    """
    Adds a subtle, elegant inner border highlight to give the icon
    a refined, premium native app feel (macOS / iOS style).
    """
    size = img_rgba.size[0]
    supersample = 4
    ss_size = size * supersample
    radius = int(ss_size * radius_ratio)
    
    border_mask = Image.new("L", (ss_size, ss_size), 0)
    draw = ImageDraw.Draw(border_mask)
    draw.rounded_rectangle(
        [int(1.5 * supersample), int(1.5 * supersample), ss_size - int(1.5 * supersample), ss_size - int(1.5 * supersample)],
        radius=radius - int(1.5 * supersample),
        outline=255,
        width=int(1.8 * supersample)
    )
    border_mask = border_mask.resize((size, size), Image.Resampling.LANCZOS)
    
    # White with slight opacity
    highlight = Image.new("RGBA", (size, size), (255, 255, 255, 0))
    highlight_np = np.zeros((size, size, 4), dtype=np.uint8)
    
    # Linear fade from top (opacity ~40) to bottom (opacity ~10)
    y, x = np.ogrid[:size, :size]
    fade = 1.0 - (y / float(size))
    alpha = (30 + 35 * fade).astype(np.uint8)
    
    highlight_np[:, :, 0] = 255
    highlight_np[:, :, 1] = 255
    highlight_np[:, :, 2] = 255
    highlight_np[:, :, 3] = alpha
    
    hl_img = Image.fromarray(highlight_np, "RGBA")
    border_layer = Image.new("RGBA", (size, size), (0, 0, 0, 0))
    border_layer.paste(hl_img, (0, 0), border_mask)
    
    return Image.alpha_composite(img_rgba, border_layer)

def process_and_round_icon(src_path, crop_box, output_path, target_size=1024, radius_ratio=0.225):
    img = Image.open(src_path).convert("RGB")
    cropped = img.crop(crop_box)
    resized = cropped.resize((target_size, target_size), Image.Resampling.LANCZOS)
    
    # Convert to RGBA
    rgba_img = resized.convert("RGBA")
    
    # Apply rounded corner mask
    mask = create_squircle_mask(target_size, radius_ratio)
    rgba_img.putalpha(mask)
    
    # Add subtle inner highlight
    final_img = add_subtle_inner_border(rgba_img, radius_ratio)
    
    final_img.save(output_path, "PNG", optimize=True)
    print(f"Generated: {output_path} ({target_size}x{target_size})")
    return final_img

def main():
    base_dir = r"e:\idea work\EnvSwitch"
    
    # 1. Primary Recommendation: Image 3 (Sleek infinity dynamic switch flow)
    img3_src = r"C:\Users\HUIDADA\.gemini\antigravity\brain\0f6af744-2722-4a3d-8dfd-852967ca1f3a\envswitch_logo_icon_v2_1794988121390.jpg"
    # Transition bounds: X: [128, 895], Y: [127, 895]
    # To keep exact 1:1, let's use box [127, 127, 896, 896] (size 769x769)
    logo_primary = process_and_round_icon(
        img3_src,
        (127, 127, 896, 896),
        os.path.join(base_dir, "logo.png"),
        target_size=1024
    )
    
    # Generate multi-resolution icons for dev convenience (512, 256, 128, 64, 32)
    sizes = [512, 256, 128, 64, 32]
    for s in sizes:
        resized = logo_primary.resize((s, s), Image.Resampling.LANCZOS)
        resized.save(os.path.join(base_dir, f"logo_{s}.png"), "PNG", optimize=True)
        print(f"Generated: logo_{s}.png")
        
    # Generate ICO file for Windows application / tray icon / web favicon
    ico_sizes = [(256, 256), (128, 128), (64, 64), (48, 48), (32, 32), (16, 16)]
    logo_primary.save(os.path.join(base_dir, "favicon.ico"), format="ICO", sizes=ico_sizes)
    print("Generated: favicon.ico")
    
    # 2. Alternative 1: Image 2 (Nested cycle switch loops)
    img2_src = r"C:\Users\HUIDADA\.gemini\antigravity\brain\0f6af744-2722-4a3d-8dfd-852967ca1f3a\envswitch_logo_icon_1794988052825.jpg"
    # Transition bounds: X: [145, 878], Y: [145, 878] -> size 733x733
    process_and_round_icon(
        img2_src,
        (145, 145, 878, 878),
        os.path.join(base_dir, "logo_alt_nested.png"),
        target_size=1024
    )
    
    # Clean up test file if present
    test_file = os.path.join(base_dir, "test_logo.png")
    if os.path.exists(test_file):
        os.remove(test_file)

if __name__ == "__main__":
    main()
