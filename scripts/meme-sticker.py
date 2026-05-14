#!/usr/bin/env python3
"""Create a meme-style WhatsApp sticker from an image/sticker input.
Usage: meme-sticker.py <input> <output.webp> <top text> [bottom text]
"""
import sys
from PIL import Image, ImageDraw, ImageFont, ImageOps, ImageSequence


def load_font(size):
    for path in (
        "/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf",
        "/usr/share/fonts/truetype/liberation2/LiberationSans-Bold.ttf",
        "/usr/share/fonts/truetype/freefont/FreeSansBold.ttf",
    ):
        try:
            return ImageFont.truetype(path, size)
        except Exception:
            pass
    return ImageFont.load_default()


def fit_canvas(im):
    im = ImageOps.exif_transpose(im).convert("RGBA")
    im.thumbnail((512, 512), Image.Resampling.LANCZOS)
    canvas = Image.new("RGBA", (512, 512), (0, 0, 0, 0))
    canvas.alpha_composite(im, ((512 - im.width) // 2, (512 - im.height) // 2))
    return canvas


def wrap_text(draw, text, font, max_width):
    words = text.split()
    if not words:
        return []
    lines, cur = [], ""
    for word in words:
        trial = word if not cur else cur + " " + word
        bbox = draw.textbbox((0, 0), trial, font=font, stroke_width=2)
        if bbox[2] - bbox[0] <= max_width or not cur:
            cur = trial
        else:
            lines.append(cur)
            cur = word
    if cur:
        lines.append(cur)
    return lines[:4]


def draw_centered(draw, y, text, font, top=True):
    lines = wrap_text(draw, text, font, 470)
    if not lines:
        return
    line_h = font.size + 8
    if not top:
        y = y - line_h * len(lines)
    for line in lines:
        bbox = draw.textbbox((0, 0), line, font=font, stroke_width=3)
        x = (512 - (bbox[2] - bbox[0])) // 2
        draw.text((x, y), line, font=font, fill="white", stroke_width=3, stroke_fill="black")
        y += line_h


def main():
    if len(sys.argv) < 4:
        print("usage: meme-sticker.py <input> <output.webp> <top text> [bottom text]", file=sys.stderr)
        return 2
    inp, out, top = sys.argv[1], sys.argv[2], sys.argv[3]
    bottom = sys.argv[4] if len(sys.argv) > 4 else ""

    im = Image.open(inp)
    try:
        im = next(ImageSequence.Iterator(im)).copy()
    except Exception:
        pass
    canvas = fit_canvas(im)
    draw = ImageDraw.Draw(canvas)
    font = load_font(44 if bottom else 54)
    draw_centered(draw, 18 if bottom else 220, top, font, top=True)
    if bottom:
        draw_centered(draw, 494, bottom, font, top=False)
    canvas.save(out, "WEBP", quality=82, method=6)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
