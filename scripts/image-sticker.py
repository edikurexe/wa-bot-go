#!/usr/bin/env python3
"""Convert image to WhatsApp-style sticker: center square crop, 512x512, rounded corners."""
import sys
from PIL import Image, ImageDraw, ImageOps


def main():
    if len(sys.argv) < 3:
        print("usage: image-sticker.py <input> <output>", file=sys.stderr)
        return 2

    inp, out = sys.argv[1], sys.argv[2]
    im = Image.open(inp)
    im = ImageOps.exif_transpose(im).convert("RGBA")

    w, h = im.size
    side = min(w, h)
    left = (w - side) // 2
    top = (h - side) // 2
    im = im.crop((left, top, left + side, top + side))
    im = im.resize((512, 512), Image.Resampling.LANCZOS)

    radius = 40
    mask = Image.new("L", (512, 512), 0)
    draw = ImageDraw.Draw(mask)
    draw.rounded_rectangle((0, 0, 512, 512), radius=radius, fill=255)
    im.putalpha(mask)

    im.save(out, "WEBP", quality=80, method=6)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
