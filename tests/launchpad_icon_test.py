#!/usr/bin/env python3
"""Read-only check of the installed app's Launchpad cache; no database writes."""
import plistlib
import sqlite3
import subprocess
import tempfile
import unittest
from pathlib import Path
ROOT = Path(__file__).resolve().parents[1]

class LaunchpadIcon(unittest.TestCase):
    def test_cached_icon_matches_installed_bundle(self):
        directory = subprocess.check_output(['getconf', 'DARWIN_USER_DIR'], text=True).strip()
        database = Path(directory) / 'com.apple.dock.launchpad/db/db'
        if not database.exists():
            self.skipTest('This macOS version has no Launchpad database')
        with sqlite3.connect('file:' + str(database) + '?mode=ro', uri=True) as connection:
            rows = connection.execute('SELECT apps.item_id, items.parent_id, items.ordering, image_cache.image_data FROM apps JOIN items ON items.rowid=apps.item_id LEFT JOIN image_cache ON image_cache.item_id=apps.item_id WHERE bundleid=?', ('dev.cham.midrive',)).fetchall()
        self.assertEqual(len(rows), 1, 'Expected one installed Launchpad entry')
        self.assertIsNotNone(rows[0][3], 'Launchpad has not generated the icon yet')
        bundle = Path.home() / 'Applications/米盘.app'
        info = plistlib.loads((bundle / 'Contents/Info.plist').read_bytes())
        asset = bundle / 'Contents/Resources' / (info['CFBundleIconFile'] + '.icns')
        with tempfile.TemporaryDirectory() as folder:
            cached = Path(folder) / 'icon.png'
            cached.write_bytes(rows[0][3])
            result = subprocess.run(['swift', str(ROOT / 'tests/LaunchpadIconTests.swift'), str(cached), str(asset)], capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        print(result.stdout.strip())
        print('Launchpad item, folder and position:', *rows[0][:3])

if __name__ == '__main__':
    unittest.main(verbosity=2)
