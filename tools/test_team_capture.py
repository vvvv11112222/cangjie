import json
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch
from unittest.mock import MagicMock
import team_capture as capture
import team_web


class TeamCaptureTests(unittest.TestCase):
    def test_windows_acl_helper_does_not_inherit_powershell7_modules(self):
        with tempfile.TemporaryDirectory() as temp,patch.dict(capture.os.environ,{'PSModulePath':'incompatible-PS7-modules'}),\
             patch.object(capture.subprocess,'run',return_value=MagicMock(returncode=0)) as run:
            capture.secure_private_storage(Path(temp))
            if capture.os.name=='nt':
                self.assertNotIn('psmodulepath',{name.casefold() for name in run.call_args.kwargs['env']})
    def test_gateway_keeps_internal_services_private_and_redirects_old_entry(self):
        with tempfile.TemporaryDirectory() as temp:
            state=Path(temp)
            config=dict(ports=dict(api=18088,web=8088),capture=dict(ip='192.168.1.23',https=8443,ca=8081,
                media=8889,udp=8189,control=9997,control_token='test-control',event_token='test-event',ffmpeg='ffmpeg',ffprobe='ffprobe'))
            capture.generate(config,state)
            gateway=(state/'Caddyfile').read_text(encoding='utf-8')
            self.assertIn('handle /internal/* {\n        respond 404',gateway)
            self.assertIn('redir https://192.168.1.23:8443{uri}',gateway)
            self.assertIn('header_down Location ^/ /media/',gateway)
            self.assertNotIn('test-control',gateway)
            media=json.loads((state/'mediamtx.yml').read_text(encoding='utf-8'))
            self.assertEqual(media['apiAddress'],'127.0.0.1:9997')
            self.assertFalse(media['webrtcIPsFromInterfaces'])
            self.assertFalse(media['pathDefaults']['overridePublisher'])
            self.assertEqual(media['pathDefaults']['recordDeleteAfter'],'0s')
            env=capture.environment(config,state)
            self.assertEqual(env['PUBLIC_ORIGIN'],env['CAPTURE_PUBLIC_URL'])
            self.assertEqual(env['CAPTURE_LIVE_VISION_ENABLED'],'true')

    def test_live_recording_prevents_service_stop(self):
        with tempfile.TemporaryDirectory() as temp,patch.object(team_web,'records',return_value={'api':{'pid':1}}),\
             patch.object(team_web,'alive',return_value=True),patch.object(team_web,'sql',side_effect=['0','1']),\
             patch('capture_dev.identity') as terminate:
            with self.assertRaisesRegex(RuntimeError,'Phone capture is active'):
                team_web.stop({'capture':{'ip':'192.168.1.23'}},Path(temp))
            terminate.assert_not_called()


if __name__=='__main__':unittest.main()
