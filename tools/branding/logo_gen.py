"""Generate the varco logo: a two-panel gateway with two request flows passing through it and a check in the gap.

The design follows the trasloco family: flat facets separated by thin gaps, a
monospace wordmark, light and dark variants, transparent backgrounds.

Usage: python tools/branding/logo_gen.py docs/assets [preview.html]
The optional preview page must not be committed. PNG output needs Pillow and
a bold monospace TTF (Consolas Bold on Windows, DejaVu Sans Mono Bold elsewhere).
"""
import base64, os, sys

OUT = sys.argv[1]

LIGHT = {
    "left": ("#2B7DE9", "#1A5FC4"),
    "right": ("#1C3D6E", "#10294F"),
    "flow": ("#2F86F6", "#16BFA0"),
    "check": "#16BFA0",
    "ink": "#10294F",
    "paper": "#F4F7FB",
}
DARK = {
    "left": ("#4A93FF", "#2D72E0"),
    "right": ("#DCE5F2", "#AFC0D8"),
    "flow": ("#4A93FF", "#2DD4B3"),
    "check": "#2DD4B3",
    "ink": "#F2F5FA",
    "paper": "#0D1117",
}

# Gateway panels (128 x 128 icon box). Each panel has an outer and an inner
# facet; the right panel mirrors the left one around x = 64.
LEFT_OUTER = [(24, 16), (39, 26), (39, 105), (24, 96)]
LEFT_INNER = [(39, 26), (54, 36), (54, 114), (39, 105)]


def mirror(points, cx=64):
    return [(2 * cx - x, y) for x, y in points]


RIGHT_OUTER, RIGHT_INNER = mirror(LEFT_OUTER), mirror(LEFT_INNER)
FOLDS = []  # facets are told apart by tone only; extra cuts read as a barcode
FLOWS = [60, 80]  # y of the two request flows
FLOW_X0, FLOW_X1, DOT_R, FLOW_W, FLOW_GAP = 10, 116, 6, 5, 10
CHECK = [(57, 44), (63, 50), (72, 40)]
FACET_GAP = 3.4


def pts(ps, dx=0):
    return " ".join(f"{x + dx:.2f},{y:.2f}" for x, y in ps)


def icon(c, uid, dx=0, flows=True):
    """SVG fragment for the icon, shifted horizontally by dx."""
    cuts = "".join(
        f'<line x1="{a[0] + dx}" y1="{a[1]}" x2="{b[0] + dx}" y2="{b[1]}" stroke-width="{FACET_GAP}"/>' for a, b in FOLDS
    )
    if flows:
        cuts += "".join(
            f'<line x1="{FLOW_X0 + dx}" y1="{y}" x2="{FLOW_X1 + dx}" y2="{y}" stroke-width="{FLOW_GAP}"/>' for y in FLOWS
        )
    mask = (
        f'<mask id="m{uid}" maskUnits="userSpaceOnUse"><rect x="-20" y="-20" width="{200 + dx}" height="200" fill="#fff"/>'
        f'<g stroke="#000" stroke-linecap="butt">{cuts}</g></mask>'
    )
    panels = (
        f'<g mask="url(#m{uid})">'
        f'<polygon fill="{c["left"][0]}" points="{pts(LEFT_OUTER, dx)}"/>'
        f'<polygon fill="{c["left"][1]}" points="{pts(LEFT_INNER, dx)}"/>'
        f'<polygon fill="{c["right"][0]}" points="{pts(RIGHT_OUTER, dx)}"/>'
        f'<polygon fill="{c["right"][1]}" points="{pts(RIGHT_INNER, dx)}"/></g>'
    )
    out = f"<defs>{mask}</defs>{panels}"
    if flows:
        for y, color in zip(FLOWS, c["flow"]):
            out += (
                f'<g stroke="{color}" stroke-width="{FLOW_W}" stroke-linecap="round" stroke-linejoin="round" fill="none">'
                f'<line x1="{FLOW_X0 + dx}" y1="{y}" x2="{FLOW_X1 + dx}" y2="{y}"/>'
                f'<polyline points="{pts([(FLOW_X1 - 7, y - 7), (FLOW_X1, y), (FLOW_X1 - 7, y + 7)], dx)}"/></g>'
                f'<circle cx="{FLOW_X0 + dx}" cy="{y}" r="{DOT_R}" fill="{color}"/>'
            )
    check_w = 5 if flows else 7
    out += (
        f'<polyline points="{pts(CHECK, dx)}" fill="none" stroke="{c["check"]}" stroke-width="{check_w}" '
        f'stroke-linecap="round" stroke-linejoin="round"/>'
    )
    return out


def svg(w, h, inner):
    return (
        f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {w} {h}" role="img" aria-label="varco">'
        f"<title>varco</title>{inner}</svg>\n"
    )


WORD = (
    "font-family=\"'JetBrains Mono','SFMono-Regular',Menlo,Consolas,'Liberation Mono',monospace\" "
    'font-size="54" font-weight="700" letter-spacing="-1.5"'
)
LOGO_W = 330


def logo(c, uid):
    return svg(LOGO_W, 128, icon(c, uid) + f'<text x="150" y="86" fill="{c["ink"]}" {WORD}>varco</text>')


files = {
    "varco-icon.svg": svg(128, 128, icon(LIGHT, "il")),
    "varco-icon-dark.svg": svg(128, 128, icon(DARK, "id")),
    "varco-favicon.svg": svg(128, 128, icon(LIGHT, "fl", flows=False)),
    "varco-logo.svg": logo(LIGHT, "ll"),
    "varco-logo-dark.svg": logo(DARK, "ld"),
}
os.makedirs(OUT, exist_ok=True)
for n, content in files.items():
    with open(os.path.join(OUT, n), "w", encoding="utf-8", newline="\n") as f:
        f.write(content)


# --- PNG renderings (avatar and social preview) --------------------------
def png_outputs():
    try:
        from PIL import Image, ImageDraw, ImageFont
    except ImportError:
        print("Pillow not installed: PNG files skipped")
        return
    font_path = next(
        (p for p in ("C:/Windows/Fonts/consolab.ttf", "/usr/share/fonts/truetype/dejavu/DejaVuSansMono-Bold.ttf") if os.path.exists(p)),
        None,
    )
    regular_path = next(
        (p for p in ("C:/Windows/Fonts/consola.ttf", "/usr/share/fonts/truetype/dejavu/DejaVuSansMono.ttf") if os.path.exists(p)),
        None,
    )
    SS = 4  # supersampling

    def draw_icon(img, c, ox, oy, scale):
        k = scale * SS
        tr = lambda ps: [(ox * SS + x * k, oy * SS + y * k) for x, y in ps]
        panels = Image.new("RGBA", img.size, (0, 0, 0, 0))
        d = ImageDraw.Draw(panels)
        for poly, color in ((LEFT_OUTER, c["left"][0]), (LEFT_INNER, c["left"][1]), (RIGHT_OUTER, c["right"][0]), (RIGHT_INNER, c["right"][1])):
            d.polygon(tr(poly), fill=color)
        for a, b in FOLDS:
            d.line(tr([a, b]), fill=(0, 0, 0, 0), width=round(FACET_GAP * k))
        for y in FLOWS:
            d.line(tr([(FLOW_X0, y), (FLOW_X1, y)]), fill=(0, 0, 0, 0), width=round(FLOW_GAP * k))
        img.alpha_composite(panels)
        d = ImageDraw.Draw(img)
        w = round(FLOW_W * k)
        for y, color in zip(FLOWS, c["flow"]):
            d.line(tr([(FLOW_X0, y), (FLOW_X1, y)]), fill=color, width=w)
            d.line(tr([(FLOW_X1 - 7, y - 7), (FLOW_X1, y), (FLOW_X1 - 7, y + 7)]), fill=color, width=w, joint="curve")
            for px, py in tr([(FLOW_X1, y), (FLOW_X1 - 7, y - 7), (FLOW_X1 - 7, y + 7)]):
                d.ellipse([px - w / 2, py - w / 2, px + w / 2, py + w / 2], fill=color)
            cx, cy = tr([(FLOW_X0, y)])[0]
            r = DOT_R * k
            d.ellipse([cx - r, cy - r, cx + r, cy + r], fill=color)
        d.line(tr(CHECK), fill=c["check"], width=w, joint="curve")
        for px, py in tr([CHECK[0], CHECK[-1]]):
            d.ellipse([px - w / 2, py - w / 2, px + w / 2, py + w / 2], fill=c["check"])

    def finish(img, name, size):
        img.convert("RGB").resize(size, Image.LANCZOS).save(os.path.join(OUT, name), optimize=True)

    c = LIGHT
    avatar = Image.new("RGBA", (512 * SS, 512 * SS), c["paper"])
    draw_icon(avatar, c, (512 - 3.0 * 128) / 2, (512 - 3.0 * 130) / 2 - 2, 3.0)
    finish(avatar, "varco-avatar-512.png", (512, 512))

    if not font_path:
        print("no bold monospace font found: social preview skipped")
        return
    social = Image.new("RGBA", (1280 * SS, 640 * SS), c["paper"])
    word = ImageFont.truetype(font_path, 118 * SS)
    tag = ImageFont.truetype(font_path, 34 * SS)
    sub = ImageFont.truetype(regular_path or font_path, 25 * SS)
    d = ImageDraw.Draw(social)
    word_w = d.textlength("varco", font=word)
    icon_px, gap = 128 * 1.45, 40
    total = icon_px * SS + gap * SS + word_w
    x0 = (1280 * SS - total) / 2 / SS
    draw_icon(social, c, x0, 140, 1.45)
    d = ImageDraw.Draw(social)
    d.text(((x0 + icon_px + gap) * SS, 232 * SS), "varco", font=word, fill=c["ink"], anchor="ls")
    for text, font, y, color in (
        ("Prove the new ingress answers like the old one", tag, 420, c["ink"]),
        ("plan · diff · run — route by route, before the DNS cutover", sub, 482, "#4A5A73"),
    ):
        d.text((640 * SS, y * SS), text, font=font, fill=color, anchor="ms")
    finish(social, "varco-social-preview.png", (1280, 640))


png_outputs()


def img(content, h):
    return f'<img style="height:{h}px" src="data:image/svg+xml;base64,{base64.b64encode(content.encode()).decode()}">'


def panel(bg, items):
    return f'<div style="background:{bg};padding:22px;border-radius:12px;display:flex;align-items:flex-end;gap:22px;flex-wrap:wrap">{items}</div>'


i_l, i_d, fav = files["varco-icon.svg"], files["varco-icon-dark.svg"], files["varco-favicon.svg"]
html = (
    "<!doctype html><meta charset=utf-8><title>varco concept</title>"
    '<body style="margin:0;padding:22px;background:#e6e9ee;display:grid;gap:14px">'
    + panel("#fff", img(i_l, 160) + img(i_l, 64) + img(i_l, 32) + img(fav, 32) + img(fav, 16))
    + panel("#0d1117", img(i_d, 160) + img(i_d, 64) + img(i_d, 32))
    + panel("#fff", img(files["varco-logo.svg"], 96) + img(files["varco-logo.svg"], 40))
    + panel("#0d1117", img(files["varco-logo-dark.svg"], 96) + img(files["varco-logo-dark.svg"], 40))
    + "</body>"
)
if len(sys.argv) > 2:
    open(sys.argv[2], "w", encoding="utf-8").write(html)
print("ok")
