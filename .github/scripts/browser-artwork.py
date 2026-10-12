"""Generate deterministic, synthetic album covers for Actions browser validation."""

from pathlib import Path
import struct
import zlib


def cover(path, variant):
    size = 320
    palettes = [
        ((35, 89, 100), (237, 176, 121), (151, 47, 73)),
        ((190, 205, 163), (54, 78, 58), (235, 224, 193)),
        ((103, 65, 88), (230, 193, 198), (62, 43, 66)),
        ((207, 104, 71), (247, 214, 170), (44, 93, 111)),
    ]
    background, foreground, accent = palettes[variant % len(palettes)]
    pixels = bytearray()
    for y in range(size):
        pixels.append(0)
        for x in range(size):
            color = background
            if 24 <= x < 296 and 24 <= y < 296:
                color = foreground
            if (x - 160) ** 2 + (y - 160) ** 2 < 104 ** 2:
                color = accent
            if 146 <= x < 174:
                color = background
            pixels.extend(color)

    def chunk(kind, data):
        return struct.pack(">I", len(data)) + kind + data + struct.pack(">I", zlib.crc32(kind + data))

    header = struct.pack(">IIBBBBB", size, size, 8, 2, 0, 0, 0)
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_bytes(b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", header) + chunk(b"IDAT", zlib.compress(pixels)) + chunk(b"IEND", b""))


if __name__ == "__main__":
    root = Path(".ci")
    for variant in range(4):
        cover(root / "artwork" / f"{variant}.png", variant)
    cover(root / "source/Artist/Album/cover.png", 0)
