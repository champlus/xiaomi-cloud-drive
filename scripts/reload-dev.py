#!/usr/bin/env python3
"""Replace the development build without interrupting active uploads."""
import json
import os
from pathlib import Path
import re
import signal
import subprocess
import time
import urllib.request

root=Path(__file__).resolve().parents[1]
support=Path.home()/'Library/Application Support/米盘'
runtime_file=support/'runtime.json'
launch_label=f'gui/{os.getuid()}/dev.cham.midrive'
launch_plist=Path.home()/'Library/LaunchAgents/dev.cham.midrive.plist'
app=Path.home()/'Applications/米盘.app'
pending=[]
# Even a process blocked before runtime.json exists can have recovery records.
for record in (support/'待恢复文件').glob('*.json'):
    try: pending.append(json.loads(record.read_text()))
    except (OSError,ValueError): raise SystemExit('无法核对待恢复传输，请先在米盘中检查。')
if runtime_file.exists():
    runtime=json.loads(runtime_file.read_text())
    request=urllib.request.Request(f'http://127.0.0.1:{runtime["port"]}/api/status',headers={'Authorization':'Bearer '+runtime['token']})
    try:
        pending.extend(json.load(urllib.request.urlopen(request,timeout=5)).get('pending',[]))
    except OSError:
        pass
if any(item.get('state') in ('receiving','uploading') for item in pending):
    raise SystemExit('文件正在传输，请完成后更新。')

restore_launch_job=subprocess.run(['launchctl','print',launch_label],capture_output=True).returncode==0
if restore_launch_job: subprocess.run(['launchctl','bootout',launch_label],check=True)
mountpoint=Path.home()/'小米云盘'
mounts=subprocess.check_output(['/sbin/mount'],text=True)
if ' on '+str(mountpoint)+' ' in mounts:
    subprocess.run(['/sbin/umount',str(mountpoint)],check=True)
pids=set()
for name in ['米盘','midrive-service']:
    executable=app/'Contents/MacOS'/name
    processes=subprocess.run(['pgrep','-f','^'+re.escape(str(executable))+'( |$)'],text=True,capture_output=True)
    pids.update(int(value) for value in processes.stdout.split())
for pid in pids:
    try: os.kill(pid,signal.SIGTERM)
    except ProcessLookupError: pass
for _ in range(100):
    alive=[]
    for pid in pids:
        try: os.kill(pid,0);alive.append(pid)
        except ProcessLookupError: pass
    if not alive: break
    time.sleep(.05)
else:
    # These are verified processes from this installed bundle, with no active
    # upload records. A blocked Keychain call may not respond to SIGTERM.
    for pid in alive:
        try: os.kill(pid,signal.SIGKILL)
        except ProcessLookupError: pass
    time.sleep(.1)
install=[str(root/'scripts/install.sh')]
if restore_launch_job and launch_plist.exists(): install.append('--no-open')
subprocess.run(install,check=True)
if restore_launch_job and launch_plist.exists(): subprocess.run(['launchctl','bootstrap',f'gui/{os.getuid()}',str(launch_plist)],check=True)
startup_deadline=time.monotonic()+60
while time.monotonic()<startup_deadline:
    try:
        current=json.loads(runtime_file.read_text())
        request=urllib.request.Request(f'http://127.0.0.1:{current["port"]}/api/status',headers={'Authorization':'Bearer '+current['token']})
        with urllib.request.urlopen(request,timeout=2) as response:
            json.load(response)
        break
    except (OSError,ValueError,KeyError):
        time.sleep(.1)
else:
    raise SystemExit('新版服务未能启动，请打开米盘检查。')
