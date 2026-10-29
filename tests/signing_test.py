#!/usr/bin/env python3
import subprocess
import importlib.util
import json
import os
import re
import tempfile
from unittest.mock import patch
from pathlib import Path
import unittest

class Signing(unittest.TestCase):
    def test_local_development_build_has_a_stable_identity(self):
        app=Path(__file__).resolve().parents[1]/'dist/米盘.app'
        for binary in [app,app/'Contents/MacOS/midrive-service',app/'Contents/MacOS/mipan']:
            result=subprocess.run(['codesign','-d','-r-',str(binary)],capture_output=True,text=True)
            self.assertEqual(result.returncode,0)
            requirement=result.stdout+result.stderr
            self.assertNotIn('designated => cdhash',requirement,'ad-hoc identity changes on every build and breaks Keychain access')
            self.assertIn('identifier "dev.cham.midrive',requirement)
            self.assertTrue('anchor' in requirement or 'certificate' in requirement)

    def signer(self):
        path=Path(__file__).resolve().parents[1]/'scripts/sign-app.py'
        spec=importlib.util.spec_from_file_location('mipan_signer',path)
        module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module);return module

    def test_missing_configured_identity_never_falls_back_to_ad_hoc(self):
        signer=self.signer()
        with tempfile.TemporaryDirectory() as d,patch.dict(os.environ,{'MIDRIVE_SIGN_IDENTITY':'','CI':''}):
            signer.CONFIG=Path(d)/'signing.json';signer.CONFIG.write_text(json.dumps({'identity':'A'*40}))
            with patch('subprocess.check_output',return_value='  1) '+'A'*40+' "Local Signing"\n'):
                self.assertEqual(signer.resolve_identity(),'A'*40)
            with patch('subprocess.check_output',return_value='0 valid identities found'):
                with self.assertRaises(SystemExit):signer.resolve_identity()

    def test_different_code_versions_keep_same_designated_requirement(self):
        identity=self.signer().resolve_identity()
        if identity=='-':self.skipTest('stable local identity required')
        requirements=[];hashes=[]
        with tempfile.TemporaryDirectory() as d:
            for i in range(2):
                binary=Path(d)/f'version-{i}'
                subprocess.run(['clang','-x','c','-','-o',str(binary)],input=f'int main(void) {{ return {i}; }}'.encode(),check=True,capture_output=True)
                subprocess.run(['codesign','--force','--sign',identity,'--timestamp=none','--identifier','dev.cham.midrive.signing-test',str(binary)],check=True,capture_output=True)
                r=subprocess.run(['codesign','-d','-r-','--verbose=4',str(binary)],capture_output=True,text=True,check=True)
                metadata=r.stdout+r.stderr
                requirements.append(next(line for line in metadata.splitlines() if 'designated =>' in line))
                hashes.append(re.search(r'CDHash=([a-f0-9]+)',metadata).group(1))
        self.assertEqual(requirements[0],requirements[1],'updating code must retain the existing Keychain identity')
        self.assertNotEqual(hashes[0],hashes[1],'test must compare genuinely different executable versions')

if __name__=='__main__':unittest.main(verbosity=2)
