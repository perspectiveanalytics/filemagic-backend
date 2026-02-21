package scripts

import _ "embed"

//go:embed pdf_editor.py
var PDFEditorScript []byte

//go:embed font_convert.py
var FontConvertScript []byte
