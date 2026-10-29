#!/usr/bin/env python3
"""Checks built public release assets; run after scripts/package.sh."""
import hashlib
import plistlib
from pathlib import Path
import platform
import unittest
import tempfile
import subprocess
import zipfile

ROOT=Path(__file__).resolve().parents[1]
ARCH=platform.machine()
DIST=ROOT/'dist'
VERSION=plistlib.loads((ROOT/'macos/Info.plist').read_bytes())['CFBundleShortVersionString']

class Release(unittest.TestCase):
    def test_built_version_matches_release_source(self):
        built=plistlib.loads((DIST/'米盘.app/Contents/Info.plist').read_bytes())
        self.assertEqual(built['CFBundleShortVersionString'],VERSION)

    def test_app_archive_contains_only_the_app(self):
        archive=DIST/f'MiPan-v{VERSION}-macos-{ARCH}.zip'
        with zipfile.ZipFile(archive) as package:
            names=package.namelist()
            self.assertIn('米盘.app/Contents/MacOS/米盘',names)
            self.assertIn('米盘.app/Contents/MacOS/midrive-service',names)
            self.assertIn('米盘.app/Contents/MacOS/mipan',names)
            self.assertTrue(all(n.startswith(('米盘.app/','__MACOSX/')) for n in names))
            self.assertFalse(any('runtime.json' in n or 'connection.json' in n or '.log' in n for n in names))
    def test_archive_extracts_to_a_valid_signed_app(self):
        archive=DIST/f'MiPan-v{VERSION}-macos-{ARCH}.zip'
        with tempfile.TemporaryDirectory() as folder:
            subprocess.run(['ditto','-x','-k',str(archive),folder],check=True)
            result=subprocess.run(['codesign','--verify','--deep','--strict',str(Path(folder)/'米盘.app')],capture_output=True,text=True)
            self.assertEqual(result.returncode,0,result.stderr)
    def test_checksums_cover_the_app_and_cli(self):
        entries={line.split(maxsplit=1)[1].strip():line.split()[0] for line in (DIST/'SHA256SUMS.txt').read_text().splitlines()}
        for name in [f'MiPan-v{VERSION}-macos-{ARCH}.zip',f'mipan-macos-{ARCH}']:
            self.assertEqual(hashlib.sha256((DIST/name).read_bytes()).hexdigest(),entries[name])
if __name__=='__main__':unittest.main(verbosity=2)
