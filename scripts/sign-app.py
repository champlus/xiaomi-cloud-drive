#!/usr/bin/env python3
"""Sign all app components with one persistent local identity."""
import json
import os
from pathlib import Path
import re
import subprocess
import sys

CONFIG=Path.home()/'Library/Application Support/米盘构建/signing.json'

def resolve_identity():
    identity=os.environ.get('MIDRIVE_SIGN_IDENTITY')
    if not identity and CONFIG.exists():
        identity=json.loads(CONFIG.read_text()).get('identity')
    if not identity and os.environ.get('CI')=='true': identity='-'
    if not identity:
        raise SystemExit('未配置稳定签名身份。请设置 MIDRIVE_SIGN_IDENTITY，或保存到 ~/Library/Application Support/米盘构建/signing.json；临时测试可显式使用 MIDRIVE_SIGN_IDENTITY=-。')
    if identity!='-':
        found=subprocess.check_output(['security','find-identity','-v','-p','codesigning'],text=True)
        identities=re.findall(r'\b([0-9A-Fa-f]{40})\s+"([^"\n]+)"',found)
        matches=[fingerprint for fingerprint,name in identities if identity.lower()==fingerprint.lower() or identity==name]
        if len(matches)!=1:raise SystemExit('配置的签名身份不可用或不唯一；已停止构建，避免退回临时签名而丢失钥匙串授权。')
        identity=matches[0]
    else:
        print('使用临时签名：此构建不保证跨版本继承钥匙串授权。',file=sys.stderr)
    return identity

def sign_app(app,identity):
    components=[(app/'Contents/MacOS/midrive-service','dev.cham.midrive.service'),(app/'Contents/MacOS/mipan','dev.cham.midrive.cli'),(app,'dev.cham.midrive')]
    for target,identifier in components:
        subprocess.run(['codesign','--force','--sign',identity,'--timestamp=none','--identifier',identifier,str(target)],check=True)
    subprocess.run(['codesign','--verify','--deep','--strict',str(app)],check=True)

if __name__=='__main__':
    if len(sys.argv)!=2:raise SystemExit('用法：sign-app.py <米盘.app>')
    sign_app(Path(sys.argv[1]),resolve_identity())
