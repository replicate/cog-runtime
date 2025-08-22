import json
import os.path
import signal
import socket
import subprocess
import sys
import time
import urllib.request
import uuid
from contextlib import closing
from pathlib import Path
from typing import Dict, List, Optional

from coglet import file_runner

ipc_port: Optional[int] = None
ipc_popen: Optional[subprocess.Popen[bytes]] = None


def find_free_port() -> int:
    with closing(socket.socket(socket.AF_INET, socket.SOCK_STREAM)) as s:
        s.bind(('', 0))
        s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        return s.getsockname()[1]


def setup_module():
    # Webhook simulates /_ipc endpoint of Go server for receiving Python runner status updates
    cwd = str(Path(__file__).absolute().parent)
    env = os.environ.copy()
    global ipc_port, ipc_popen
    ipc_port = find_free_port()
    env['PORT'] = str(ipc_port)
    ipc_popen = subprocess.Popen(['python3', 'webhook.py'], cwd=cwd, env=env)


def teardown_module():
    global ipc_popen
    ipc_popen.terminate()


class FileRunnerTest:
    def __init__(
        self,
        tmp_path: Path,
        predictor: str,
        env: Optional[Dict[str, str]] = None,
        max_concurrency: int = 1,
        predictor_class: str = 'Predictor',
    ):
        # Runner
        runner_env = os.environ.copy()
        if env is not None:
            runner_env.update(env)
        runner_env['PYTHONPATH'] = str(Path(__file__).absolute().parent.parent)
        global ipc_port
        self.name = f'runner-{uuid.uuid4()}'
        cmd = [
            sys.executable,
            '-m',
            'coglet',
            '--name',
            self.name,
            '--ipc-url',
            f'http://localhost:{ipc_port}/_ipc',
            '--working-dir',
            tmp_path.as_posix(),
        ]
        conf_file = os.path.join(tmp_path, 'config.json')
        with open(conf_file, 'w') as f:
            conf = {
                'module_name': f'tests.runners.{predictor}',
                'predictor_name': predictor_class,
                'max_concurrency': max_concurrency,
            }
            json.dump(conf, f)
        self.runner = subprocess.Popen(
            cmd, env=runner_env, stdout=subprocess.PIPE, stderr=subprocess.PIPE
        )

    def statuses(self) -> List[str]:
        global ipc_port
        resp = urllib.request.urlopen(f'http://localhost:{ipc_port}/_requests')
        requests = json.loads(resp.read()) or []
        statuses = []
        for r in requests:
            if r['path'] != '/_ipc':
                continue
            body = r['body']
            if body['name'] != self.name:
                continue
            statuses.append(body['status'])
        return statuses

    def stop(self, exit_code: int = 0) -> None:
        c = self.runner.wait()
        assert c == exit_code


def wait_for_file(path, exists: bool = True) -> None:
    while True:
        time.sleep(0.1)
        if os.path.exists(path) == exists:
            time.sleep(0.2)  # Wait for IPC
            return


def test_file_runner(tmp_path):
    rt = FileRunnerTest(tmp_path, 'sleep', env={'SETUP_SLEEP': '1'})

    openapi_file = os.path.join(tmp_path, 'openapi.json')
    wait_for_file(openapi_file)

    setup_result_file = os.path.join(tmp_path, 'setup_result.json')
    wait_for_file(setup_result_file)
    with open(setup_result_file) as f:
        setup_result = json.load(f)
    assert setup_result['status'] == 'succeeded'
    assert rt.statuses() == [file_runner.FileRunner.IPC_READY]

    req_file = os.path.join(tmp_path, 'request-a.json')
    resp_file = os.path.join(tmp_path, 'response-a-00000.json')
    with open(req_file, 'w') as f:
        json.dump({'input': {'i': 1, 's': 'bar'}}, f)
    wait_for_file(req_file, exists=False)
    assert rt.statuses() == [
        file_runner.FileRunner.IPC_READY,
        file_runner.FileRunner.IPC_BUSY,
    ]
    wait_for_file(resp_file)
    assert rt.statuses() == [
        file_runner.FileRunner.IPC_READY,
        file_runner.FileRunner.IPC_BUSY,
        file_runner.FileRunner.IPC_OUTPUT,
        file_runner.FileRunner.IPC_READY,
    ]

    with open(resp_file, 'r') as f:
        resp = json.load(f)
    assert resp['status'] == 'succeeded'
    assert resp['output'] == '*bar*'

    stop_file = os.path.join(tmp_path, 'stop')
    Path(stop_file).touch()
    rt.stop()


def test_file_runner_setup_failed(tmp_path):
    rt = FileRunnerTest(tmp_path, 'sleep', predictor_class='SetupFailingPredictor')

    openapi_file = os.path.join(tmp_path, 'openapi.json')
    wait_for_file(openapi_file)

    setup_result_file = os.path.join(tmp_path, 'setup_result.json')
    wait_for_file(setup_result_file)
    with open(setup_result_file) as f:
        setup_result = json.load(f)
    assert setup_result['status'] == 'failed'
    assert rt.statuses() == []
    rt.stop(1)


def test_file_runner_predict_failed(tmp_path):
    rt = FileRunnerTest(
        tmp_path, 'sleep', predictor_class='PredictionFailingPredictorWithTiming'
    )

    openapi_file = os.path.join(tmp_path, 'openapi.json')
    wait_for_file(openapi_file)

    setup_result_file = os.path.join(tmp_path, 'setup_result.json')
    wait_for_file(setup_result_file)
    with open(setup_result_file) as f:
        setup_result = json.load(f)
    assert setup_result['status'] == 'succeeded'
    assert rt.statuses() == [file_runner.FileRunner.IPC_READY]

    req_file = os.path.join(tmp_path, 'request-a.json')
    resp_file = os.path.join(tmp_path, 'response-a-00000.json')
    with open(req_file, 'w') as f:
        json.dump({'input': {'i': 1, 's': 'bar'}}, f)
    wait_for_file(req_file, exists=False)
    assert rt.statuses() == [
        file_runner.FileRunner.IPC_READY,
        file_runner.FileRunner.IPC_BUSY,
    ]
    wait_for_file(resp_file)
    assert rt.statuses() == [
        file_runner.FileRunner.IPC_READY,
        file_runner.FileRunner.IPC_BUSY,
        file_runner.FileRunner.IPC_OUTPUT,
        file_runner.FileRunner.IPC_READY,
    ]

    with open(resp_file, 'r') as f:
        resp = json.load(f)
    assert resp['status'] == 'failed'
    assert resp['error'] == 'prediction failed'

    stop_file = os.path.join(tmp_path, 'stop')
    Path(stop_file).touch()
    rt.stop()


def test_file_runner_predict_canceled(tmp_path):
    rt = FileRunnerTest(tmp_path, 'sleep')

    openapi_file = os.path.join(tmp_path, 'openapi.json')
    wait_for_file(openapi_file)

    setup_result_file = os.path.join(tmp_path, 'setup_result.json')
    wait_for_file(setup_result_file)
    with open(setup_result_file) as f:
        setup_result = json.load(f)
    assert setup_result['status'] == 'succeeded'
    time.sleep(1)
    assert rt.statuses() == [file_runner.FileRunner.IPC_READY]

    req_file = os.path.join(tmp_path, 'request-a.json')
    resp_file = os.path.join(tmp_path, 'response-a-00000.json')
    with open(req_file, 'w') as f:
        json.dump({'input': {'i': 60, 's': 'bar'}}, f)
    wait_for_file(req_file, exists=False)
    assert rt.statuses() == [
        file_runner.FileRunner.IPC_READY,
        file_runner.FileRunner.IPC_BUSY,
    ]
    os.kill(rt.runner.pid, signal.SIGUSR1)
    wait_for_file(resp_file)
    assert rt.statuses() == [
        file_runner.FileRunner.IPC_READY,
        file_runner.FileRunner.IPC_BUSY,
        file_runner.FileRunner.IPC_OUTPUT,
        file_runner.FileRunner.IPC_READY,
    ]

    with open(resp_file, 'r') as f:
        resp = json.load(f)
    assert resp['status'] == 'canceled'

    stop_file = os.path.join(tmp_path, 'stop')
    Path(stop_file).touch()
    rt.stop()
