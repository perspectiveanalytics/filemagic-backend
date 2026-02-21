#!/usr/bin/env python3
"""Font format converter using fonttools.

Usage: font_convert.py <input> <output> <format>
Supported formats: ttf, otf, woff, woff2
"""
import sys
import os


def otf_to_ttf(font):
    """Convert CFF outlines to TrueType (glyf) outlines."""
    from fontTools.cu2qu.cu2qu import curve_to_quadratic
    from fontTools.pens.cu2quPen import Cu2QuPen
    from fontTools.pens.ttGlyphPen import TTGlyphPen
    from fontTools.ttLib import TTFont, newTable

    glyph_order = font.getGlyphOrder()
    cff = font["CFF "]
    cff_font = cff.cff.topDictIndex[0]
    char_strings = cff_font.CharStrings

    glyf_table = newTable("glyf")
    glyf_table.glyphs = {}
    glyf_table.glyphOrder = glyph_order

    for glyph_name in glyph_order:
        pen = TTGlyphPen(None)
        cu2qu_pen = Cu2QuPen(pen, max_err=1.0, reverse_direction=True)
        try:
            char_strings[glyph_name].draw(cu2qu_pen)
            glyf_table[glyph_name] = pen.glyph()
        except Exception:
            # Fallback: empty glyph
            pen = TTGlyphPen(None)
            glyf_table[glyph_name] = pen.glyph()

    del font["CFF "]
    if "CFF2" in font:
        del font["CFF2"]
    font["glyf"] = glyf_table

    # Ensure loca table exists
    font["loca"] = newTable("loca")

    # Update head table flags for TrueType
    if "head" in font:
        font["head"].glyphDataFormat = 0

    font.sfntVersion = "\x00\x01\x00\x00"  # TrueType signature


def ttf_to_otf(font):
    """Convert TrueType (glyf) outlines to CFF outlines."""
    from fontTools.pens.t2CharStringPen import T2CharStringPen
    from fontTools.cffLib import CFFFontSet, TopDict, TopDictIndex, PrivateDict, CharStrings, Index
    from fontTools.ttLib import newTable

    glyph_order = font.getGlyphOrder()
    glyph_set = font.getGlyphSet()

    # Build CFF charstrings
    charstrings = {}
    for glyph_name in glyph_order:
        width = glyph_set[glyph_name].width
        pen = T2CharStringPen(width, glyph_set)
        try:
            glyph_set[glyph_name].draw(pen)
            charstrings[glyph_name] = pen.getCharString()
        except Exception:
            from fontTools.misc.psCharStrings import T2CharString
            charstrings[glyph_name] = T2CharString()

    # Build CFF table
    cff = CFFFontSet()
    cff.major = 1
    cff.minor = 0
    global_subrs = Index()
    cff.GlobalSubrs = global_subrs
    top_dict = TopDict()
    top_dict.charset = glyph_order
    private = PrivateDict()
    top_dict.Private = private
    cs = CharStrings(
        file=None, charset=glyph_order, globalSubrs=global_subrs,
        private=private, fdSelect=None, fdArray=None
    )
    for name, charstring in charstrings.items():
        charstring.private = private
        charstring.globalSubrs = global_subrs
        cs[name] = charstring
    top_dict.CharStrings = cs
    font_name = font.get("name").getDebugName(6) or "Font"
    top_dict.rawDict["FullName"] = font_name
    cff.fontNames = [font_name]
    tdi = TopDictIndex()
    tdi.append(top_dict)
    cff.topDictIndex = tdi

    cff_table = newTable("CFF ")
    cff_table.cff = cff

    del font["glyf"]
    if "loca" in font:
        del font["loca"]
    font["CFF "] = cff_table

    font.sfntVersion = "OTTO"


def main():
    if len(sys.argv) != 4:
        print("Usage: font_convert.py <input> <output> <format>", file=sys.stderr)
        sys.exit(1)

    input_path, output_path, target_format = sys.argv[1], sys.argv[2], sys.argv[3]

    if not os.path.isfile(input_path):
        print(f"Input file not found: {input_path}", file=sys.stderr)
        sys.exit(1)

    target_format = target_format.lower()
    if target_format not in ("ttf", "otf", "woff", "woff2"):
        print(f"Unsupported format: {target_format}", file=sys.stderr)
        sys.exit(1)

    from fontTools.ttLib import TTFont

    font = TTFont(input_path)

    if target_format == "woff":
        font.flavor = "woff"
    elif target_format == "woff2":
        font.flavor = "woff2"
    elif target_format in ("ttf", "otf"):
        font.flavor = None  # Remove WOFF wrapper if present

    # Handle TTF <-> OTF outline conversion
    is_cff = "CFF " in font or "CFF2" in font

    if target_format == "ttf" and is_cff:
        otf_to_ttf(font)
    elif target_format == "otf" and not is_cff:
        ttf_to_otf(font)

    font.save(output_path)
    font.close()


if __name__ == "__main__":
    main()
