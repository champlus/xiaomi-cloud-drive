#!/usr/bin/env python3
"""Exercise the updater without touching running apps, mounts or credentials."""
import io
import json
from pathlib import Path
import runpy
import subprocess
import tempfile
import unittest
from unittest.mock import patch

class Reload(unittest.TestCase):
    def test_restored_launch_agent_is_the_only_app_launcher(self):
        script=Path(__file__).resolve().parents[1]/'scripts/reload-dev.py'
        with tempfile.TemporaryDirectory() as d:
            home=Path(d); support=home/'Library/Application Support/米盘'; support.mkdir(parents=True)
            (support/'runtime.json').write_text(json.dumps({'port':12345,'token':'synthetic-local-token','pid':424242}))
            plist=home/'Library/LaunchAgents/dev.cham.midrive.plist';plist.parent.mkdir(parents=True);plist.write_text('synthetic launch agent')
            commands=[]
            def run(args,**kwargs):
                commands.append(args)
                return subprocess.CompletedProcess(args,0,stdout='424243\n' if args[0]=='pgrep' else '')
            def kill(pid,sig):
                if sig==0:raise ProcessLookupError()
            with patch.object(Path,'home',return_value=home), patch('subprocess.run',side_effect=run), patch('subprocess.check_output',return_value=''), patch('urllib.request.urlopen',side_effect=lambda *a,**k:io.BytesIO(b'{"pending":[]}')), patch('os.kill',side_effect=kill):
                runpy.run_path(str(script),run_name='__main__')
            installs=[a for a in commands if a[0].endswith('/scripts/install.sh')]
            self.assertEqual(len(installs),1)
            self.assertIn('--no-open',installs[0], 'installer and launchd must not race to launch two instances')
            self.assertEqual(sum(a[:2]==['launchctl','bootstrap'] for a in commands),1)

    def test_active_transfer_is_not_interrupted_by_update(self):
        script=Path(__file__).resolve().parents[1]/'scripts/reload-dev.py'
        with tempfile.TemporaryDirectory() as d:
            home=Path(d);support=home/'Library/Application Support/米盘';support.mkdir(parents=True)
            (support/'runtime.json').write_text(json.dumps({'port':12345,'token':'synthetic-local-token','pid':424242}))
            response=io.BytesIO(b'{"pending":[{"state":"uploading"}]}')
            with patch.object(Path,'home',return_value=home), patch('subprocess.run') as run, patch('os.kill') as kill, patch('urllib.request.urlopen',return_value=response):
                with self.assertRaisesRegex(SystemExit,'正在传输'):
                    runpy.run_path(str(script),run_name='__main__')
                run.assert_not_called();kill.assert_not_called()

    def test_update_can_replace_an_app_blocked_before_service_ready(self):
        script=Path(__file__).resolve().parents[1]/'scripts/reload-dev.py'
        with tempfile.TemporaryDirectory() as d:
            home=Path(d);support=home/'Library/Application Support/米盘';support.mkdir(parents=True)
            plist=home/'Library/LaunchAgents/dev.cham.midrive.plist';plist.parent.mkdir(parents=True);plist.write_text('synthetic launch agent')
            commands=[]
            def run(args,**kwargs):
                commands.append(args)
                if args[0].endswith('/scripts/install.sh'):
                    (support/'runtime.json').write_text(json.dumps({'port':12345,'token':'synthetic-token','pid':424244}))
                return subprocess.CompletedProcess(args,0,stdout='424243\n' if args[0]=='pgrep' else '')
            def kill(pid,sig):
                if sig==0:raise ProcessLookupError()
            with patch.object(Path,'home',return_value=home),patch('subprocess.run',side_effect=run),patch('subprocess.check_output',return_value=''),patch('os.kill',side_effect=kill) as stop,patch('urllib.request.urlopen',side_effect=lambda *a,**k:io.BytesIO(b'{"pending":[]}')):
                runpy.run_path(str(script),run_name='__main__')
            self.assertTrue(any(a[:2]==['launchctl','bootout'] for a in commands),'blocked old app was not stopped before installation')
            self.assertTrue(stop.called,'blocked process survived update')
            self.assertIn('--no-open',next(a for a in commands if a[0].endswith('/scripts/install.sh')))

if __name__=='__main__':unittest.main(verbosity=2)
