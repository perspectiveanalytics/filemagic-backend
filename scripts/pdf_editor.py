#!/usr/bin/env python3
"""PDF editor script using PyMuPDF.

Usage: python3 pdf_editor.py <input.pdf> <output.pdf> <config.json>

Config JSON fields (all optional):
  pages        - list of 1-indexed page numbers for reorder/extract
  rotations    - dict of "outputPageIndex": angle (90/180/270)
  watermark    - {text, fontSize, color, opacity, rotation, position}
  pageNumbers  - {format, position, fontSize, color, startFrom, margin}
  redactions   - [{page (0-indexed), rect [x,y,w,h]}]
"""
import json
import sys

try:
    import pymupdf
except ImportError:
    import fitz as pymupdf


def apply_pages(doc, pages):
    """Reorder/extract pages. pages is a list of 1-indexed page numbers."""
    if not pages:
        return
    selection = [p - 1 for p in pages if 1 <= p <= len(doc)]
    if not selection:
        raise ValueError("no valid pages in selection")
    doc.select(selection)


def apply_rotations(doc, rotations):
    """Apply rotations. rotations maps "outputPagePos" (1-indexed) -> angle."""
    if not rotations:
        return
    for page_str, angle in rotations.items():
        idx = int(page_str) - 1
        angle = int(angle)
        if angle not in (90, 180, 270):
            continue
        if 0 <= idx < len(doc):
            page = doc[idx]
            page.set_rotation((page.rotation + angle) % 360)


def position_to_point(position, page_rect, text, fontsize, margin=30):
    """Convert a named position to a (x, y) point on the page."""
    w, h = page_rect.width, page_rect.height
    # Approximate text width (rough heuristic: 0.5 * fontsize per char)
    tw = len(text) * fontsize * 0.35
    th = fontsize

    positions = {
        "center": (w / 2 - tw / 2, h / 2 + th / 2),
        "top-left": (margin, margin + th),
        "top-center": (w / 2 - tw / 2, margin + th),
        "top-right": (w - tw - margin, margin + th),
        "bottom-left": (margin, h - margin),
        "bottom-center": (w / 2 - tw / 2, h - margin),
        "bottom-right": (w - tw - margin, h - margin),
    }
    return positions.get(position, positions["center"])


def clamp(v, lo, hi):
    """Clamp value to [lo, hi] range."""
    return max(lo, min(hi, v))


def apply_watermark(doc, wm):
    """Add text watermark to all pages."""
    if not wm or not wm.get("text"):
        return

    text = str(wm["text"])[:200]
    fontsize = clamp(float(wm.get("fontSize", 60)), 1, 200)
    color = tuple(clamp(float(c), 0, 1) for c in wm.get("color", [0.5, 0.5, 0.5])[:3])
    opacity = clamp(float(wm.get("opacity", 0.3)), 0, 1)
    rotation = clamp(float(wm.get("rotation", 45)), -360, 360)
    position = wm.get("position", "center")

    for page in doc:
        rect = page.rect
        x, y = position_to_point(position, rect, text, fontsize)
        point = pymupdf.Point(x, y)

        morph = None
        if rotation != 0:
            center = pymupdf.Point(rect.width / 2, rect.height / 2)
            morph = (center, pymupdf.Matrix(rotation))

        page.insert_text(
            point=point,
            text=text,
            fontsize=fontsize,
            color=color,
            fill_opacity=opacity,
            rotate=0,
            morph=morph,
            overlay=True,
        )


def apply_page_numbers(doc, pn):
    """Add page numbers to all pages."""
    if not pn:
        return

    fmt = str(pn.get("format", "{n} / {total}"))[:200]
    position = pn.get("position", "bottom-center")
    fontsize = clamp(float(pn.get("fontSize", 10)), 1, 200)
    color = tuple(clamp(float(c), 0, 1) for c in pn.get("color", [0, 0, 0])[:3])
    start_from = int(clamp(float(pn.get("startFrom", 1)), 0, 9999))
    margin = clamp(float(pn.get("margin", 30)), 0, 100)
    total = len(doc)

    for i, page in enumerate(doc):
        n = start_from + i
        text = fmt.replace("{n}", str(n)).replace("{total}", str(total))
        x, y = position_to_point(position, page.rect, text, fontsize, margin)

        page.insert_text(
            point=(x, y),
            text=text,
            fontsize=fontsize,
            color=color,
            overlay=True,
        )


def apply_redactions(doc, redactions):
    """Apply redactions. Each redaction has page (0-indexed) and rect [x,y,w,h]."""
    if not redactions:
        return

    pages_to_redact = set()
    for r in redactions:
        page_idx = r.get("page", 0)
        rect_data = r.get("rect")
        if rect_data is None or len(rect_data) != 4:
            continue
        if page_idx < 0 or page_idx >= len(doc):
            continue

        x, y, w, h = rect_data
        rect = pymupdf.Rect(x, y, x + w, y + h)
        page = doc[page_idx]
        page.add_redact_annot(rect, fill=(0, 0, 0))
        pages_to_redact.add(page_idx)

    for page_idx in pages_to_redact:
        # PDF_REDACT_IMAGE_REMOVE: removes any image overlapping a redaction rect
        # entirely, rather than decoding → blanking pixels → re-encoding.
        # This avoids triggering image decoders (JBIG2, JPEG2000, JPEG) on
        # potentially crafted images, eliminating the decoder attack surface.
        doc[page_idx].apply_redactions(
            images=pymupdf.PDF_REDACT_IMAGE_REMOVE
        )

    if pages_to_redact:
        doc.scrub()


def main():
    if len(sys.argv) != 4:
        print(f"Usage: {sys.argv[0]} <input.pdf> <output.pdf> <config.json>",
              file=sys.stderr)
        sys.exit(1)

    input_path, output_path, config_path = sys.argv[1], sys.argv[2], sys.argv[3]

    with open(config_path, "r") as f:
        config = json.load(f)

    doc = pymupdf.open(input_path)

    # Operations applied in order
    apply_pages(doc, config.get("pages"))
    apply_rotations(doc, config.get("rotations"))
    apply_watermark(doc, config.get("watermark"))
    apply_page_numbers(doc, config.get("pageNumbers"))
    apply_redactions(doc, config.get("redactions"))

    doc.save(output_path, garbage=4, deflate=True)
    doc.close()


if __name__ == "__main__":
    main()
