#!/usr/bin/env python3
"""Reinstall the locally built app, after checking that no transfer is active."""
import json
import os
from pathlib import Path
import signal
import subprocess
import time
import urllib.request

root=Path(__file__).resolve().parents[1]
support=Path.home()/'Library/Application Support/米盘'
runtime_file=support/'runtime.json'
launch_label=f'gui/{os.getuid()}/dev.cham.midrive'
launch_plist=Path.home()/'Library/LaunchAgents/dev.cham.midrive.plist'
restore_launch_job=False
if runtime_file.exists():
    runtime=json.loads(runtime_file.read_text())
    request=urllib.request.Request(f'http://127.0.0.1:{runtime["port"]}/api/status',headers={'Authorization':'Bearer '+runtime['token']})
    try:
        state=json.load(urllib.request.urlopen(request,timeout=5))
    except OSError:
        state={'pending':[]}
    if any(item['state'] in ('receiving','uploading') for item in state['pending']):
        raise SystemExit('文件正在传输，请完成后更新。')
    restore_launch_job=subprocess.run(['launchctl','print',launch_label],capture_output=True).returncode==0
    if restore_launch_job: subprocess.run(['launchctl','bootout',launch_label],check=True)
    mountpoint=Path.home()/'小米云盘'
    mounts=subprocess.check_output(['/sbin/mount'],text=True)
    if ' on '+str(mountpoint)+' ' in mounts:
        subprocess.run(['/sbin/umount',str(mountpoint)],check=True)
    processes=subprocess.run(['pgrep','-f','^'+str(Path.home()/'Applications/米盘.app/Contents/MacOS/米盘')],text=True,capture_output=True)
    pids=[int(v) for v in processes.stdout.split()]
    pids.append(runtime['pid'])
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
    else: raise SystemExit('旧进程尚未退出，请稍后重试。')
subprocess.run([str(root/'scripts/install.sh')],check=True)
if restore_launch_job and launch_plist.exists(): subprocess.run(['launchctl','bootstrap',f'gui/{os.getuid()}',str(launch_plist)],check=True)
