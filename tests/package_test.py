#!/usr/bin/env python3
import subprocess
from pathlib import Path
import unittest

class Package(unittest.TestCase):
    def test_installed_bundle_has_valid_seal(self):
        app=Path.home()/'Applications/米盘.app'
        result=subprocess.run(['codesign','--verify','--deep','--strict',str(app)],capture_output=True,text=True)
        self.assertEqual(result.returncode,0,result.stderr)
    def test_user_supplied_icon_is_installed_verbatim(self):
        source=Path(__file__).resolve().parents[1]/'macos/AppIcon.png'
        installed=Path.home()/'Applications/米盘.app/Contents/Resources/AppIcon.png'
        self.assertEqual(source.read_bytes(),installed.read_bytes())
if __name__=='__main__':unittest.main(verbosity=2)
