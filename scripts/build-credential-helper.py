#!/usr/bin/env python3
"""Keep the signed Keychain owner byte-stable across UI and toolchain updates."""
import hashlib, importlib.util, json, os, platform, shutil, subprocess, sys
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1]
spec=importlib.util.spec_from_file_location('signer',ROOT/'scripts/sign-app.py')
signer=importlib.util.module_from_spec(spec);spec.loader.exec_module(signer)
identity=signer.resolve_identity()
source=ROOT/'native/credential-helper.c'
key=hashlib.sha256(source.read_bytes()+identity.encode()+platform.machine().encode()).hexdigest()
cache=Path.home()/'Library/Application Support/米盘构建/credential-helper'/key
cache.parent.mkdir(parents=True,exist_ok=True)
if not cache.exists():
    temp=cache.with_suffix(f'.{os.getpid()}.tmp')
    try:
        subprocess.run(['clang','-O2','-Wno-deprecated-declarations',str(source),'-framework','Security','-framework','CoreFoundation','-o',str(temp)],check=True)
        subprocess.run(['codesign','--sign',identity,'--timestamp=none','--options','runtime','--identifier','dev.cham.midrive.credentials',str(temp)],check=True)
        temp.replace(cache)
    finally:
        temp.unlink(missing_ok=True)
requirement = 'identifier "dev.cham.midrive.credentials"' + (f' and certificate leaf = H"{identity}"' if identity != '-' else '')
subprocess.run(['codesign','--verify','--strict','-R','='+requirement,str(cache)],check=True)
manifest=json.loads((ROOT/'native/credential-helper.json').read_text())
if identity==manifest['identity'] and platform.machine()==manifest['architecture']:
    if hashlib.sha256(source.read_bytes()).hexdigest()!=manifest['sourceSHA256'] or hashlib.sha256(cache.read_bytes()).hexdigest()!=manifest['binarySHA256']:
        raise SystemExit('固定凭证组件与已发布版本不一致。请从原构建缓存恢复相同的签名组件；变更组件必须先实现登录迁移，不能自动替换。')
shutil.copy2(cache,sys.argv[1])
