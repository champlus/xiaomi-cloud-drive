#!/usr/bin/env python3
"""Opt-in real-cloud acceptance tests. Only touches its own randomly named folder."""
import base64
import hashlib
import json
import os
from pathlib import Path
import unittest
import urllib.error
import urllib.parse
import urllib.request
import uuid

@unittest.skipUnless(os.environ.get('MIDRIVE_LIVE') == '1', 'set MIDRIVE_LIVE=1 to test the signed-in account')
class RealCloud(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.runtime=json.loads((Path.home()/'Library/Application Support/米盘/runtime.json').read_text())
        cls.base=f'http://127.0.0.1:{cls.runtime["port"]}/dav'
        cls.auth='Basic '+base64.b64encode(('xiaomi:'+cls.runtime['password']).encode()).decode()
        cls.folder='/米盘验收-'+uuid.uuid4().hex[:10]
        status,_,_=cls.dav('MKCOL',cls.folder)
        if status!=201: raise RuntimeError(f'Cannot create acceptance sandbox: HTTP {status}')
    @classmethod
    def dav(cls,method,path,body=None,**headers):
        request=urllib.request.Request(cls.base+urllib.parse.quote(path),data=body,method=method,headers={'Authorization':cls.auth,**headers})
        try: response=urllib.request.urlopen(request,timeout=180)
        except urllib.error.HTTPError as e: response=e
        with response: return response.status,response.read(),response.headers
    @classmethod
    def tearDownClass(cls):
        status,_,_=cls.dav('DELETE',cls.folder)
        if status!=204: raise RuntimeError(f'Test sandbox cleanup failed: HTTP {status}')
    def test_01_upload_download_unicode_and_ranges(self):
        data=('你好，Finder！\n'*100).encode()
        status,body,_=self.dav('PUT',self.folder+'/中文文件.txt',data)
        self.assertEqual(status,201,body.decode(errors='replace'))
        status,body,_=self.dav('GET',self.folder+'/中文文件.txt')
        self.assertEqual(status,200);self.assertEqual(body,data)
        status,body,_=self.dav('GET',self.folder+'/中文文件.txt',Range='bytes=5-25')
        self.assertEqual(status,206);self.assertEqual(body,data[5:26])
    def test_02_empty_file(self):
        status,body,_=self.dav('PUT',self.folder+'/empty.txt',b'')
        self.assertEqual(status,201,body.decode(errors='replace'))
        status,body,_=self.dav('GET',self.folder+'/empty.txt')
        self.assertEqual(status,200);self.assertEqual(body,b'')
    def test_03_multiblock_upload_and_overwrite(self):
        data=os.urandom(4*1024*1024+123)
        target=self.folder+'/blocks.bin'
        status,body,_=self.dav('PUT',target,data)
        self.assertEqual(status,201,body.decode(errors='replace'))
        status,body,_=self.dav('GET',target)
        self.assertEqual(status,200);self.assertEqual(hashlib.sha256(body).digest(),hashlib.sha256(data).digest())
        status,body,_=self.dav('PUT',target,b'replaced')
        self.assertEqual(status,204,body.decode(errors='replace'))
        _,body,_=self.dav('GET',target);self.assertEqual(body,b'replaced')
    def test_04_move_copy_and_delete(self):
        target=self.folder+'/original.txt';self.assertEqual(self.dav('PUT',target,b'move me')[0],201)
        new=self.folder+'/renamed.txt'
        status,body,_=self.dav('MOVE',target,Destination=self.base+urllib.parse.quote(new))
        self.assertEqual(status,201,body.decode(errors='replace'))
        self.assertEqual(self.dav('COPY',new,Destination=self.base+urllib.parse.quote(self.folder+'/copy.txt'))[0],201)
        self.assertEqual(self.dav('DELETE',new)[0],204)
    def test_05_native_finder_mount(self):
        folder=Path.home()/'小米云盘'/self.folder.lstrip('/')/'Finder 读写'
        folder.mkdir()
        file=folder/'native.txt';data='从 macOS WebDAV 文件系统写入\n'.encode();file.write_bytes(data)
        self.assertEqual(file.read_bytes(),data)
        renamed=folder/'renamed.txt';file.rename(renamed)
        self.assertEqual(renamed.read_bytes(),data)
        renamed.unlink();folder.rmdir()

if __name__=='__main__': unittest.main(verbosity=2)
