#!/usr/bin/env python3
"""Render the AII OS app icon.

The mark is the identity's presence orb from the dashboard, not a new
invention: same gradients, same accents (--acc #8b6cff, --acc2 #3fa4ff,
core rgb(20,16,60)) read out of theme.css and layout.css. An icon that
disagreed with the interface would be a second, competing identity for
the same thing.

Pure stdlib on purpose — no PIL, no cairo. The build host needs nothing
installed to reproduce it.
"""
import math, struct, sys, zlib

ACC = (139, 108, 255)     # --acc  #8b6cff
ACC2 = (63, 164, 255)     # --acc2 #3fa4ff
CORE = (20, 16, 60)       # orb outer stop
BG0 = (10, 9, 22)         # near --bg0, the app's ground
BG1 = (22, 18, 52)        # slight lift so the plate is not flat black


def lerp(a, b, t):
    return tuple(a[i] + (b[i] - a[i]) * t for i in range(3))


def smooth(t):
    t = max(0.0, min(1.0, t))
    return t * t * (3 - 2 * t)


def orb_color(dx, dy, r):
    """The .orb background, evaluated per pixel.

    radial-gradient(circle at 50% 45%, acc .95, acc2 .75 @46%, core @100%)
    plus radial-gradient(circle at 34% 30%, white .85, transparent 18%).
    """
    # main gradient, centred at 50%/45% of the orb box.
    #
    # The stops carry ALPHA in the CSS (.95 acc, .75 acc2, .9 core) and
    # they sit over the dark ground. Ignoring that renders a bright
    # cyan ball; compositing it is what mutes the blue and lets the
    # violet read, which is what the dashboard actually shows.
    cx, cy = 0.0, -0.05 * 2 * r
    d = math.hypot(dx - cx, dy - cy) / r
    if d <= 0.46:
        t = smooth(d / 0.46)
        col = lerp(ACC, ACC2, t)
        alpha = 0.95 + (0.75 - 0.95) * t
    else:
        t = smooth((d - 0.46) / 0.54)
        col = lerp(ACC2, CORE, t)
        alpha = 0.75 + (0.90 - 0.75) * t
    col = lerp(CORE, col, alpha)

    # specular highlight at 34%/30%
    hx, hy = (0.34 - 0.5) * 2 * r, (0.30 - 0.5) * 2 * r
    hd = math.hypot(dx - hx, dy - hy) / (r * 0.36)
    if hd < 1.0:
        col = lerp(col, (255, 255, 255), 0.85 * (1 - smooth(hd)) ** 1.5)
    return col


def render(size):
    px = bytearray()
    c = size / 2.0
    # macOS leaves a margin; the orb occupies the classic ~0.78 of the
    # square so it sits correctly beside stock icons in the Dock.
    R = size * 0.305
    plate = size * 0.4165          # squircle half-extent
    radius = size * 0.1855         # corner radius
    ss = 2                          # supersample factor
    for y in range(size):
        px.append(0)               # PNG filter byte: none
        for x in range(size):
            rs = gs = bs = as_ = 0.0
            for oy in range(ss):
                for ox in range(ss):
                    sx = x + (ox + 0.5) / ss - c
                    sy = y + (oy + 0.5) / ss - c
                    # rounded-square plate (superellipse-ish)
                    ax, ay = abs(sx), abs(sy)
                    k = plate - radius
                    if ax <= k and ay <= k:
                        inside = 1.0
                    else:
                        qx = max(ax - k, 0.0)
                        qy = max(ay - k, 0.0)
                        dd = math.hypot(qx, qy)
                        inside = 1.0 - smooth((dd - radius) / 1.5 + 0.5)
                    if inside <= 0.0:
                        continue
                    # plate: vertical wash
                    t = (sy + plate) / (2 * plate)
                    col = lerp(BG1, BG0, smooth(t))
                    # outer glow of the orb on the plate
                    d = math.hypot(sx, sy)
                    if d < R * 2.1:
                        g = (1 - smooth((d - R) / (R * 1.1))) if d > R else 1.0
                        col = lerp(col, ACC, 0.20 * max(0.0, g))
                    # the orb
                    if d <= R:
                        edge = 1.0 - smooth((d - (R - 1.2)) / 1.2)
                        col = lerp(col, orb_color(sx, sy, R), max(0.0, min(1.0, edge)))
                    # the halo ring (.orb::after), at inset -9/86 of the orb box
                    ring = R * 1.105
                    rd = abs(d - ring)
                    if rd < max(1.0, size / 512.0):
                        col = lerp(col, ACC, 0.22 * (1 - rd / max(1.0, size / 512.0)))
                    rs += col[0] * inside
                    gs += col[1] * inside
                    bs += col[2] * inside
                    as_ += 255.0 * inside
            n = ss * ss
            px += bytes((int(rs / n + 0.5), int(gs / n + 0.5), int(bs / n + 0.5), int(as_ / n + 0.5)))
    return bytes(px)


def png(size, path):
    raw = render(size)
    def chunk(tag, data):
        c = tag + data
        return struct.pack(">I", len(data)) + c + struct.pack(">I", zlib.crc32(c) & 0xFFFFFFFF)
    ihdr = struct.pack(">IIBBBBB", size, size, 8, 6, 0, 0, 0)
    out = (b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", ihdr)
           + chunk(b"IDAT", zlib.compress(raw, 9)) + chunk(b"IEND", b""))
    open(path, "wb").write(out)


if __name__ == "__main__":
    size = int(sys.argv[1])
    png(size, sys.argv[2])
    print(f"  {sys.argv[2]} ({size}x{size})")
