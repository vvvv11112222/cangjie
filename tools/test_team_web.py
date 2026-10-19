"""Local website lifecycle boundary tests; no real database or model is started."""
import os
from pathlib import Path
import socket
import tempfile
import unittest
from unittest.mock import patch
import team_web as dev

class TeamWebTests(unittest.TestCase):
    def config(self, directory):
        runtime = directory / 'runtime'; runtime.mkdir(exist_ok=True)
        return {'go':str(runtime / 'go/bin/go.exe'),'pg_bin':str(runtime / 'pgsql/bin'), 'node':'node',
            'models':str(directory / 'models'), 'ports':{'web':8088,'api':18088,'database':55437},
            'secrets':{'database':'test-only-db','worker':'test-only-worker','login':'test-only-login'},
            'runtime':{'processor_version':'test','ffmpeg_build_sha256':'a'*64,'ffmpeg_binary':'ffmpeg',
                       'visual_model':{'revision':'b'*64}}}
    def test_deployment_pins_cpu_and_private_loopback(self):
        with tempfile.TemporaryDirectory() as temp:
            state=Path(temp); config=self.config(state)
            with patch.dict(os.environ, {'VISION_RUNTIME':'cuda','VISION_GPU_DETECTION':'1','VISION_BATCH_SIZE':'96'}), patch.object(dev,'ascii_root',side_effect=lambda p:p):
                env=dev.environment(config,state)
            self.assertEqual(env['VISION_RUNTIME'],'cpu')
            self.assertEqual(env['VISION_GPU_DETECTION'],'0')
            self.assertEqual(env['VISION_BATCH_SIZE'],'1')
            self.assertEqual(env['VISION_CPU_THREADS'],'4')
            self.assertEqual(env['VISION_ANALYSIS_ENABLED'],'1')
            self.assertEqual(env['PUBLIC_ORIGIN'],'http://127.0.0.1:8088')
            self.assertEqual(env['API_LISTEN_ADDR'],'127.0.0.1:18088')
            self.assertEqual(env['MODEL_MONTHLY_BUDGET'],'0')
    def test_running_analysis_prevents_stop(self):
        with tempfile.TemporaryDirectory() as temp, patch.object(dev,'records',return_value={'api':{'pid':1}}), \
             patch.object(dev,'alive',return_value=True), patch.object(dev,'sql',return_value='1'), \
             patch('capture_dev.identity') as terminate:
            with self.assertRaisesRegex(RuntimeError,'Analysis is running'): dev.stop({},Path(temp))
            terminate.assert_not_called()
    def test_partial_service_is_not_reported_as_running(self):
        with tempfile.TemporaryDirectory() as temp, patch.object(dev,'records',return_value={'api':{'pid':1}}), \
             patch.object(dev,'alive',return_value=True), patch.object(dev,'wait_ready') as ready:
            with self.assertRaisesRegex(RuntimeError,'partly running'): dev.start({},Path(temp))
            ready.assert_not_called()
    def test_occupied_port_is_refused_without_killing_anything(self):
        with socket.socket() as bound:
            bound.bind(('127.0.0.1',0)); bound.listen()
            with self.assertRaisesRegex(RuntimeError,'occupied'): dev.check_port(bound.getsockname()[1])
    def test_sql_uses_utf8_stdin_not_locale_dependent_arguments(self):
        with tempfile.TemporaryDirectory() as temp:
            state=Path(temp); cfg=self.config(state)
            with patch.object(dev,'pg_tools',return_value=state), patch.object(dev,'environment',return_value={}), patch('subprocess.run') as execute:
                execute.return_value.returncode=0;execute.return_value.stdout=b'1\n'
                self.assertEqual(dev.sql(cfg,state,"SELECT '本机验收'"),'1')
                self.assertEqual(execute.call_args.kwargs['input'],"SELECT '本机验收'".encode('utf-8'))
                self.assertNotIn('-c',execute.call_args.args[0])
    def test_database_and_worker_have_separate_capabilities(self):
        with tempfile.TemporaryDirectory() as temp, patch.object(dev,'ascii_root',side_effect=lambda p:p):
            state=Path(temp);env=dev.environment(self.config(state),state)
            self.assertEqual(env['WORKER_CAPABILITIES'],'probe,audio_analysis,video_analysis')
            self.assertEqual(env['WORKER_CONCURRENCY'],'1')

if __name__=='__main__': unittest.main()
