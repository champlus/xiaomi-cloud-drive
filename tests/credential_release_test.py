#!/usr/bin/env python3
"""A release must preserve the exact signed Keychain owner already in use."""
import hashlib
import json
from pathlib import Path
import platform
import plistlib
import subprocess
import unittest
import zipfile

ROOT = Path(__file__).resolve().parents[1]
class CredentialRelease(unittest.TestCase):
    def test_release_contains_the_fixed_signed_component(self):
        manifest = json.loads((ROOT / 'native/credential-helper.json').read_text())
        app = ROOT / 'dist/米盘.app'
        helper = app / 'Contents/MacOS/credential-helper'
        metadata = subprocess.run(['codesign', '-d', '-r-', str(helper)], capture_output=True, text=True, check=True)
        value = helper.read_bytes()
        if platform.machine() == manifest['architecture'] and manifest['identity'].lower() in (metadata.stdout + metadata.stderr).lower():
            self.assertEqual(hashlib.sha256(value).hexdigest(), manifest['binarySHA256'])
            self.assertEqual(hashlib.sha256((ROOT / 'native/credential-helper.c').read_bytes()).hexdigest(), manifest['sourceSHA256'])
        version = plistlib.loads((app / 'Contents/Info.plist').read_bytes())['CFBundleShortVersionString']
        with zipfile.ZipFile(ROOT / f'dist/MiPan-v{version}-macos-{platform.machine()}.zip') as archive:
            self.assertEqual(archive.read('米盘.app/Contents/MacOS/credential-helper'), value)

if __name__ == '__main__':
    unittest.main(verbosity=2)
