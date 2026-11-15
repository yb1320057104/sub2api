import multiprocessing
import os
import signal
import tempfile
import threading
import time
import unittest
from pathlib import Path
from unittest.mock import Mock, patch
from tools import openai_oauth_reauth_worker as worker


def drain_probe(config, stop):
    root = Path(config.base_url)
    (root / (config.worker_id + '.started')).touch()
    while not stop.wait(.01):
        pass
    (root / (config.worker_id + '.draining')).touch()
    deadline = time.monotonic() + 10
    while not (root / 'release').exists() and time.monotonic() < deadline:
        time.sleep(.01)
    (root / (config.worker_id + '.finished')).touch()


def run_graceful_pool_probe(root):
    worker.SHUTDOWN_REQUESTED.clear()
    signal.signal(signal.SIGTERM, worker.terminate_worker)
    config = worker.WorkerConfig(root, 'synthetic-token', 'probe', None, concurrency=2)
    with patch.object(worker, '_worker_loop', drain_probe), \
         patch.object(worker.WorkerAPI, 'runtime_concurrency', return_value=2):
        worker.run_worker_pool(config)


class RuntimeControlsTest(unittest.TestCase):
    def setUp(self):
        worker.SHUTDOWN_REQUESTED.clear()

    def config(self, root='http://127.0.0.1'):
        return worker.WorkerConfig(root, 'synthetic-token', 'probe', None, concurrency=2)

    def test_runtime_response_validates_bounds_and_preserves_env_before_configuration(self):
        api = worker.WorkerAPI(self.config())
        for value in (0, 17, 1.5, '3', True):
            with patch.object(api, '_post', return_value={'worker_concurrency': value}), self.assertRaises(worker.WorkerError):
                api.runtime_concurrency()
        for value in (1, 4, 16):
            with patch.object(api, '_post', return_value={'worker_concurrency': value}):
                self.assertEqual(api.runtime_concurrency(), value)
        with patch.object(api, '_post', return_value={'worker_concurrency': None}):
            self.assertEqual(api.runtime_concurrency(), 2)

    def test_retiring_worker_finishes_claim_and_never_claims_again(self):
        stop = threading.Event()
        def finish_current(*_):
            stop.set()
            return True
        with patch.object(worker, 'run_once', side_effect=finish_current) as run:
            worker._worker_loop(self.config(), stop)
        run.assert_called_once()

    def test_real_processes_scale_down_without_interrupting_active_work_then_scale_up(self):
        context = multiprocessing.get_context('spawn')
        with tempfile.TemporaryDirectory() as directory:
            cfg = self.config(directory)
            children = {}
            root = Path(directory)
            def wait_for(condition):
                deadline = time.monotonic() + 10
                while not condition() and time.monotonic() < deadline:
                    time.sleep(.02)
                self.assertTrue(condition())
            try:
                with patch.object(worker, '_worker_loop', drain_probe):
                    worker.reconcile_worker_pool(context, children, cfg, 2)
                    wait_for(lambda: len(list(root.glob('*.started'))) == 2)
                    retiring = children[2][0]
                    worker.reconcile_worker_pool(context, children, cfg, 1)
                    wait_for(lambda: (root / 'probe-slot-2.draining').exists())
                    self.assertTrue(retiring.is_alive())
                    self.assertFalse((root / 'probe-slot-2.finished').exists())
                    (root / 'release').touch()
                    retiring.join(timeout=5)
                    self.assertEqual(retiring.exitcode, 0)
                    worker.reconcile_worker_pool(context, children, cfg, 1)
                    self.assertEqual(set(children), {1})
                    worker.reconcile_worker_pool(context, children, cfg, 3)
                    self.assertEqual(set(children), {1, 2, 3})
                    self.assertNotEqual(children[2][0].pid, retiring.pid)
                    wait_for(lambda: (root / 'probe-slot-3.started').exists())
            finally:
                (root / 'release').touch()
                for child, stop in children.values():
                    stop.set()
                for child, _ in children.values():
                    child.join(timeout=5)
                    if child.is_alive():
                        child.kill(); child.join()

    def test_pool_keeps_last_target_during_api_failure(self):
        cfg = self.config()
        with patch.object(worker.WorkerAPI, 'runtime_concurrency', side_effect=worker.WorkerError('offline')), \
             patch.object(worker, 'reconcile_worker_pool') as reconcile, \
             patch.object(worker.time, 'sleep', side_effect=KeyboardInterrupt):
            with self.assertRaises(KeyboardInterrupt):
                worker.run_worker_pool(cfg)
        self.assertEqual(reconcile.call_args.args[-1], 2)

    @unittest.skipUnless(os.name == 'posix', 'POSIX termination signals')
    def test_supervisor_sigterm_keeps_active_slots_until_their_callbacks_finish(self):
        context = multiprocessing.get_context('fork')
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            supervisor = context.Process(target=run_graceful_pool_probe, args=(directory,))
            supervisor.start()
            try:
                deadline = time.monotonic() + 10
                while len(list(root.glob('*.started'))) < 2 and time.monotonic() < deadline:
                    time.sleep(.02)
                self.assertEqual(len(list(root.glob('*.started'))), 2)
                os.kill(supervisor.pid, signal.SIGTERM)
                deadline = time.monotonic() + 5
                while len(list(root.glob('*.draining'))) < 2 and time.monotonic() < deadline:
                    time.sleep(.02)
                self.assertEqual(len(list(root.glob('*.draining'))), 2)
                self.assertTrue(supervisor.is_alive())
                self.assertEqual(len(list(root.glob('*.finished'))), 0)
                (root / 'release').touch()
                supervisor.join(timeout=5)
                self.assertFalse(supervisor.is_alive())
                self.assertEqual(supervisor.exitcode, 0)
                self.assertEqual(len(list(root.glob('*.finished'))), 2)
            finally:
                (root / 'release').touch()
                if supervisor.is_alive():
                    os.kill(supervisor.pid, signal.SIGTERM)
                    supervisor.join(timeout=5)
                if supervisor.is_alive():
                    supervisor.kill()
                    supervisor.join()

if __name__ == '__main__':
    unittest.main()
