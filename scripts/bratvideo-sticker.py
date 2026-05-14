#!/usr/bin/env python3
"""Create a simple animated typing/brat-style WhatsApp sticker.
Usage: bratvideo-sticker.py <output.webp> <text>
"""
import sys
from PIL import Image, ImageDraw, ImageFont


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


def wrap(draw, text, font, max_width=460):
    words = text.split()
    if not words:
        return [text]
    lines, cur = [], ""
    for word in words:
        trial = word if not cur else cur + " " + word
        bbox = draw.textbbox((0, 0), trial, font=font)
        if bbox[2] - bbox[0] <= max_width or not cur:
            cur = trial
        else:
            lines.append(cur)
            cur = word
    if cur:
        lines.append(cur)
    return lines[:7]


def render_frame(text, font):
    im = Image.new("RGBA", (512, 512), (0, 0, 0, 0))
    draw = ImageDraw.Draw(im)
    lines = wrap(draw, text, font)
    line_h = font.size + 8
    y = (512 - line_h * len(lines)) // 2
    for line in lines:
        bbox = draw.textbbox((0, 0), line, font=font)
        x = (512 - (bbox[2] - bbox[0])) // 2
        draw.text((x, y), line, font=font, fill=(255, 255, 255, 255), stroke_width=2, stroke_fill=(0, 0, 0, 255))
        y += line_h
    return im


def main():
    if len(sys.argv) < 3:
        print("usage: bratvideo-sticker.py <output.webp> <text>", file=sys.stderr)
        return 2
    out, text = sys.argv[1], sys.argv[2].strip()
    if not text:
        print("empty text", file=sys.stderr)
        return 2
    text = text[:80]
    font = load_font(52 if len(text) <= 25 else 42)
    steps = min(len(text), 18)
    frames = []
    for i in range(1, steps + 1):
        n = max(1, round(len(text) * i / steps))
        frames.append(render_frame(text[:n], font))
    frames.append(render_frame(text, font))
    durations = [90] * (len(frames) - 1) + [900]
    frames[0].save(out, "WEBP", save_all=True, append_images=frames[1:], duration=durations, loop=0, quality=82, method=6)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
