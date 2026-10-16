import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

try:
    import pymupdf
except ImportError:
    import fitz as pymupdf

SCRIPT = Path(__file__).with_name('pdf_editor.py')

class RedactionTests(unittest.TestCase):
    def run_editor(self, config, source_rotation=0):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            with pymupdf.open() as doc:
                doc.new_page(width=300, height=400).insert_text((30, 80), 'PUBLIC')
                page = doc.new_page(width=300, height=400)
                page.insert_text((30, 80), 'SECRET')
                page.set_rotation(source_rotation)
                rect = pymupdf.Rect(20, 60, 140, 90) * page.rotation_matrix
                if config.get('redactionSpace') == 'source':
                    config['redactions'] = [{'page': 1, 'rect': [rect.x0, rect.y0, rect.width, rect.height]}]
                doc.save(root / 'in.pdf')
            (root / 'config.json').write_text(json.dumps(config))
            result = subprocess.run([sys.executable, str(SCRIPT), str(root/'in.pdf'), str(root/'out.pdf'), str(root/'config.json')], capture_output=True)
            if result.returncode:
                return result.returncode, None
            with pymupdf.open(root/'out.pdf') as doc:
                self.rotations = [page.rotation for page in doc]
                return 0, [page.get_text() for page in doc]

    def test_source_redactions_survive_page_operations(self):
        for rotation in (0, 90, 180, 270):
            for pages in ([2], [2, 1], [2, 2, 1]):
                with self.subTest(rotation=rotation, pages=pages):
                    code, text = self.run_editor({'pages': pages, 'rotations': {'1': 90}, 'redactionSpace': 'source'}, rotation)
                    self.assertEqual(code, 0)
                    self.assertNotIn('SECRET', ''.join(text))
                    self.assertEqual(len(text), len(pages))
                    self.assertEqual(self.rotations, [(rotation + 90) % 360] + [rotation if p == 2 else 0 for p in pages[1:]])
                    if 1 in pages:
                        self.assertIn('PUBLIC', ''.join(text))

    def test_legacy_combined_redactions_fail_closed(self):
        code, _ = self.run_editor({'pages': [2], 'redactions': [{'page': 1, 'rect': [20,60,120,30]}]})
        self.assertNotEqual(code, 0)

    def test_invalid_redaction_fails_closed(self):
        for rect in ([0,0,0,10], [0,0,10], [0,0,float('nan'),10]):
            code, _ = self.run_editor({'redactions': [{'page': 1, 'rect': rect}]})
            self.assertNotEqual(code, 0)
        code, _ = self.run_editor({'redactions': [{'page': 8, 'rect': [0,0,10,10]}]})
        self.assertNotEqual(code, 0)

if __name__ == '__main__':
    unittest.main()
