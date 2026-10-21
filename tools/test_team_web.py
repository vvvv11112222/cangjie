"""Local website lifecycle boundary tests; no real database or model is started."""
import os
import json
from pathlib import Path
import socket
import tempfile
import unittest
from unittest.mock import MagicMock, patch
import team_web as dev

class TeamWebTests(unittest.TestCase):
    def test_existing_later_alias_is_reused_before_a_free_drive(self):
        state=Path('课堂部署')
        drives={letter:MagicMock(name=letter) for letter in ('V','W','Y','Z')}
        for letter,drive in drives.items(): drive.exists.return_value=letter in ('V','Y')
        with patch.object(dev.os,'name','nt'), patch.object(dev,'Path',side_effect=lambda p:drives[p[0]]), \
             patch.object(dev.os.path,'samefile',side_effect=lambda a,b:a is drives['Y']), patch.object(dev.subprocess,'run') as create:
            self.assertIs(dev.ascii_root(state),drives['Y'])
            create.assert_not_called()
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
    def test_standard_go_install_never_receives_cache_or_temp_writes(self):
        with tempfile.TemporaryDirectory() as temp:
            state=Path(temp); config=self.config(state)
            config['go']='C:/Program Files/Go/bin/go.exe'
            with patch.object(dev,'ascii_root',side_effect=lambda p:p) as alias:
                env=dev.environment(config,state)
            alias.assert_called_once_with(state)
            for key in ('GOCACHE','GOMODCACHE','GOTMPDIR','TMP','TEMP'):
                self.assertTrue(Path(env[key]).is_relative_to(state), key)
    def test_full_analysis_is_explicit_and_uses_private_configuration(self):
        with tempfile.TemporaryDirectory() as temp:
            state=Path(temp); config=self.config(state)
            private=state/'analysis.private.json'
            private.write_text(json.dumps({'ASR_CLOUD_ENABLED':'true','ASR_API_KEY':'synthetic-private-key','ASR_MODEL_NAME':'synthetic-asr','MODEL_MONTHLY_BUDGET':'1'}),encoding='utf-8')
            config['analysis_config']=str(private)
            with patch.object(dev,'ascii_root',side_effect=lambda p:p):
                env=dev.environment(config,state)
            self.assertEqual(env['ASR_MODEL_NAME'],'synthetic-asr')
            self.assertEqual(env['ASR_PLANNER'],str(state/'bin/audio-plan.exe'))
            private.write_text(json.dumps({'DATABASE_URL':'unapproved override'}),encoding='utf-8')
            with self.assertRaisesRegex(RuntimeError,'only string'):
                dev.environment(config,state)
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

    def test_directml_environment_does_not_inherit_another_pcs_adapter(self):
        with tempfile.TemporaryDirectory() as temp:
            state=Path(temp); config=self.config(state)
            config['vision_execution']={'engine':'directml','batch_size':16,
                'device':{'name':'test discrete GPU','device_id':2},'accelerator_root':str(state/'accelerators')}
            with patch.dict(os.environ,{'VISION_DML_DEVICE':'0','VISION_TF32':'1','VISION_BATCH_SIZE':'96'}), \
                 patch.object(dev,'ascii_root',side_effect=lambda p:p):
                env=dev.environment(config,state)
            self.assertEqual(env['VISION_DML_DEVICE'],'2')
            self.assertEqual(env['VISION_RUNTIME'],'directml')
            self.assertEqual(env['VISION_BATCH_SIZE'],'16')
            self.assertEqual(env['VISION_REQUIRE_GPU'],'1')
            self.assertEqual(env['VISION_TF32'],'0')

    def test_auto_fallback_is_explicit_and_forced_gpu_fails_closed(self):
        with tempfile.TemporaryDirectory() as temp:
            state=Path(temp); config=self.config(state); config['python']='python'
            config['vision_runtime']='auto'
            failed=MagicMock(returncode=1,stdout='{"error":"DirectML is not installed"}\n',stderr='')
            with patch.object(dev.subprocess,'run',return_value=failed):
                result=dev.select_vision(config,state)
                self.assertEqual(result['engine'],'cpu')
                self.assertIn('DirectML is not installed',result['reason'])
                self.assertIsNone(result['device'])
                config['vision_runtime']='directml'
                with self.assertRaisesRegex(RuntimeError,'要求独显'):
                    dev.select_vision(config,state)

    def test_restart_reenumerates_gpu_and_records_probe_result(self):
        with tempfile.TemporaryDirectory() as temp:
            state=Path(temp); config=self.config(state); config['python']='python';config['vision_runtime']='auto'
            config['vision_execution']={'engine':'directml','device':{'device_id':0}}
            verified={'engine':'directml','device':{'name':'current GPU','device_id':2},'batch_size':16,'probe_passed':True}
            success=MagicMock(returncode=0,stdout=json.dumps(verified)+'\n',stderr='')
            with patch.object(dev.subprocess,'run',return_value=success) as probe:
                result=dev.select_vision(config,state)
                probe.assert_called_once()
                self.assertEqual(result['device']['device_id'],2)
                self.assertEqual(dev.load(state/'vision-runtime.json')['device']['device_id'],2)

    def test_probe_timeout_does_not_claim_gpu_success(self):
        with tempfile.TemporaryDirectory() as temp:
            state=Path(temp); config=self.config(state);config['python']='python';config['vision_runtime']='auto'
            with patch.object(dev.subprocess,'run',side_effect=dev.subprocess.TimeoutExpired('probe',45)):
                result=dev.select_vision(config,state)
            self.assertEqual(result['engine'],'cpu')
            self.assertFalse(result['probe_passed'])

if __name__=='__main__': unittest.main()
