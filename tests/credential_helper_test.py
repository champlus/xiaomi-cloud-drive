#!/usr/bin/env python3
"""Synthetic item only. Never reads the user's actual Xiaomi session."""
import importlib.util,plistlib,subprocess,tempfile,uuid,unittest
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1]
class Credentials(unittest.TestCase):
 def test_changed_app_code_retains_access_and_other_callers_are_rejected(self):
  spec=importlib.util.spec_from_file_location('signer',ROOT/'scripts/sign-app.py');signer=importlib.util.module_from_spec(spec);spec.loader.exec_module(signer)
  identity=signer.resolve_identity()
  if identity=='-':self.skipTest('local stable signing identity required')
  with tempfile.TemporaryDirectory() as folder:
   folder=Path(folder);broker=folder/'broker';clients=[]
   service='dev.cham.midrive.test.'+uuid.uuid4().hex
   parent='dev.cham.midrive.credential-test-client'
   subprocess.run(['clang','-w','-O2','-DTEST_VAULT',f'-DVAULT_SERVICE="{service}"',f'-DAPP_IDENTIFIER="{parent}"',str(ROOT/'native/credential-helper.c'),'-framework','Security','-framework','CoreFoundation','-o',str(broker)],check=True)
   subprocess.run(['codesign','--sign',identity,'--timestamp=none','--options','runtime','--identifier','dev.cham.midrive.credentials',str(broker)],check=True)
   for version in [1,2]:
    app=folder/f'v{version}.app';binary=app/'Contents/MacOS/Fixture';binary.parent.mkdir(parents=True)
    (app/'Contents/Info.plist').write_bytes(plistlib.dumps({'CFBundleIdentifier':parent,'CFBundleExecutable':'Fixture','CFBundleVersion':str(version),'CFBundlePackageType':'APPL'}))
    (folder/f'Build{version}.swift').write_text(f'let fixtureBuild = {version}\n')
    subprocess.run(['swiftc','-O',str(folder/f'Build{version}.swift'),str(ROOT/'macos/CredentialBroker.swift'),str(ROOT/'tests/CredentialClient.swift'),'-o',str(binary)],check=True)
    subprocess.run(['codesign','--sign',identity,'--timestamp=none','--options','runtime',str(app)],check=True);clients.append(binary)
   hashes = [subprocess.check_output(['codesign','-d','--verbose=4',str(p)],stderr=subprocess.STDOUT) for p in clients]
   import re
   self.assertNotEqual(*[re.search(rb'CDHash=([0-9a-f]+)',value).group(1) for value in hashes])
   try:
    for client,operation in [(clients[0],'write'),(clients[0],'read'),(clients[1],'read')]:
     r=subprocess.run([str(client),str(broker),operation],capture_output=True,timeout=20)
     self.assertEqual(r.returncode,0,f'{client.parent.parent.parent.name} {operation} failed: {r.returncode}')
    rejected=subprocess.run([str(broker),'read'],input=b'GO\n',capture_output=True,timeout=20)
    self.assertEqual(rejected.returncode,77);self.assertEqual(rejected.stdout,b'')
    # A substituted child must receive no session data, even if it mimics the handshake.
    fake=folder/'impostor';captured=folder/'captured'
    fake.write_text('#!/bin/sh\nprintf "MPVAULT1\\n"\ncat > "'+str(captured)+'"\n')
    fake.chmod(0o755)
    rejected=subprocess.run([str(clients[0]),str(fake),'write'],capture_output=True,timeout=20)
    self.assertNotEqual(rejected.returncode,0)
    self.assertTrue(not captured.exists() or captured.read_bytes()==b'')
   finally:
    cleanup=subprocess.run([str(clients[0]),str(broker),'delete-test'],capture_output=True,timeout=20)
    self.assertEqual(cleanup.returncode,0,'failed to remove isolated synthetic Keychain item')
if __name__=='__main__':unittest.main(verbosity=2)
