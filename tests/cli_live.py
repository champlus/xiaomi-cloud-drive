#!/usr/bin/env python3
"""Opt-in CLI acceptance tests using an isolated real-cloud folder."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
import uuid

@unittest.skipUnless(os.environ.get('MIDRIVE_LIVE')=='1','set MIDRIVE_LIVE=1 for real-cloud tests')
class CLI(unittest.TestCase):
    binary=str(Path.home()/'.local/bin/mipan')
    @classmethod
    def run_cli(cls,*args,ok=True):
        r=subprocess.run([cls.binary,'--json',*args],capture_output=True,text=True,timeout=180)
        value=json.loads(r.stdout)
        if ok and r.returncode!=0: raise AssertionError(value)
        if not ok and r.returncode==0: raise AssertionError('command unexpectedly succeeded')
        return value
    @classmethod
    def setUpClass(cls):
        cls.folder='/米盘CLI验收-'+uuid.uuid4().hex[:10]
        cls.run_cli('mkdir',cls.folder)
    @classmethod
    def tearDownClass(cls): cls.run_cli('rm','--recursive',cls.folder)
    def test_cli_lifecycle(self):
        with tempfile.TemporaryDirectory() as d:
            source=Path(d)/'source.txt';source.write_text('hello from CLI 你好\n')
            remote=self.folder+'/agent.txt'
            self.run_cli('put',str(source),remote)
            listing=self.run_cli('ls',self.folder)['data']
            self.assertTrue(any(e['name']=='agent.txt' for e in listing))
            self.assertEqual(self.run_cli('stat',remote)['data']['size'],source.stat().st_size)
            destination=Path(d)/'download.txt';self.run_cli('get',remote,str(destination));self.assertEqual(destination.read_bytes(),source.read_bytes())
            self.run_cli('get',remote,str(destination),ok=False)
            self.run_cli('cp',remote,self.folder+'/copy.txt')
            self.run_cli('mv',self.folder+'/copy.txt',self.folder+'/renamed.txt')
            self.run_cli('rm',self.folder+'/renamed.txt')
            self.assertTrue(self.run_cli('status')['data']['connected'])
    def test_recursive_directory_upload(self):
        with tempfile.TemporaryDirectory() as d:
            source=Path(d)/'directory';source.mkdir();(source/'child.txt').write_text('nested')
            remote=self.folder+'/directory';self.run_cli('put','--recursive',str(source),remote)
            self.assertEqual(self.run_cli('stat',remote+'/child.txt')['data']['size'],6)
            self.run_cli('rm',remote,ok=False)
            self.run_cli('rm','--recursive',remote)
if __name__=='__main__':unittest.main(verbosity=2)
