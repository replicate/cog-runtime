import json
import os.path
import signal
import subprocess
import sys
import time
from pathlib import Path
from typing import Dict, List, Optional

from coglet import file_runner


def setup_signals() -> List[int]:
    signals = []

    def handler(signum, _):
        signals.append(signum)

    signal.signal(file_runner.FileRunner.SIG_OUTPUT, handler)
    signal.signal(file_runner.FileRunner.SIG_READY, handler)
    signal.signal(file_runner.FileRunner.SIG_BUSY, handler)
    return signals


def wait_for_file(path, exists: bool = True) -> None:
    while True:
        time.sleep(0.1)
        if os.path.exists(path) == exists:
            time.sleep(0.1)  # Wait for signal
            return


def wait_for_process(p: subprocess.Popen, code: int = 0) -> None:
    while True:
        time.sleep(0.1)
        c = p.poll()
        if c is not None:
            assert c == code
            return


def run_file_runner(
    tmp_path: Path, predictor: str, env: Optional[Dict[str, str]] = None
) -> subprocess.Popen:
    if env is None:
        env = {}
    env['PYTHONPATH'] = str(Path(__file__).absolute().parent.parent)
    cmd = [
        sys.executable,
        '-m',
        'coglet',
        '--working-dir',
        tmp_path.as_posix(),
    ]
    conf_file = os.path.join(tmp_path, 'config.json')
    with open(conf_file, 'w') as f:
        conf = {'module_name': f'tests.runners.{predictor}', 'class_name': 'Predictor'}
        json.dump(conf, f)
    return subprocess.Popen(
        cmd, env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE
    )


def test_file_runner(tmp_path):
    signals = setup_signals()

    env = os.environ.copy()
    env['SETUP_SLEEP'] = '1'
    p = run_file_runner(tmp_path, 'sleep', env=env)

    openapi_file = os.path.join(tmp_path, 'openapi.json')
    wait_for_file(openapi_file)

    setup_result_file = os.path.join(tmp_path, 'setup_result.json')
    wait_for_file(setup_result_file)
    with open(setup_result_file) as f:
        setup_result = json.load(f)
    assert setup_result['status'] == 'succeeded'
    assert signals == [file_runner.FileRunner.SIG_READY]

    req_file = os.path.join(tmp_path, 'request-a.json')
    resp_file = os.path.join(tmp_path, 'response-a-00000.json')
    with open(req_file, 'w') as f:
        json.dump({'input': {'i': 1, 's': 'bar'}}, f)
    wait_for_file(req_file, exists=False)
    assert signals == [
        file_runner.FileRunner.SIG_READY,
        file_runner.FileRunner.SIG_BUSY,
    ]
    wait_for_file(resp_file)
    assert signals == [
        file_runner.FileRunner.SIG_READY,
        file_runner.FileRunner.SIG_BUSY,
        file_runner.FileRunner.SIG_OUTPUT,
        file_runner.FileRunner.SIG_READY,
    ]

    with open(resp_file, 'r') as f:
        resp = json.load(f)
    assert resp['status'] == 'succeeded'
    assert resp['output'] == '*bar*'

    stop_file = os.path.join(tmp_path, 'stop')
    Path(stop_file).touch()
    wait_for_process(p)


def test_file_runner_setup_failed(tmp_path):
    signals = setup_signals()

    env = os.environ.copy()
    env['SETUP_SLEEP'] = '1'
    env['SETUP_FAILURE'] = '1'
    p = run_file_runner(tmp_path, 'sleep', env=env)

    openapi_file = os.path.join(tmp_path, 'openapi.json')
    wait_for_file(openapi_file)

    setup_result_file = os.path.join(tmp_path, 'setup_result.json')
    wait_for_file(setup_result_file)
    with open(setup_result_file) as f:
        setup_result = json.load(f)
    assert setup_result['status'] == 'failed'
    assert signals == []
    wait_for_process(p, 1)


def test_file_runner_predict_failed(tmp_path):
    signals = setup_signals()

    env = os.environ.copy()
    env['PREDICTION_FAILURE'] = '1'
    p = run_file_runner(tmp_path, 'sleep', env=env)

    openapi_file = os.path.join(tmp_path, 'openapi.json')
    wait_for_file(openapi_file)

    setup_result_file = os.path.join(tmp_path, 'setup_result.json')
    wait_for_file(setup_result_file)
    with open(setup_result_file) as f:
        setup_result = json.load(f)
    assert setup_result['status'] == 'succeeded'
    assert signals == [file_runner.FileRunner.SIG_READY]

    req_file = os.path.join(tmp_path, 'request-a.json')
    resp_file = os.path.join(tmp_path, 'response-a-00000.json')
    with open(req_file, 'w') as f:
        json.dump({'input': {'i': 1, 's': 'bar'}}, f)
    wait_for_file(req_file, exists=False)
    assert signals == [
        file_runner.FileRunner.SIG_READY,
        file_runner.FileRunner.SIG_BUSY,
    ]
    wait_for_file(resp_file)
    assert signals == [
        file_runner.FileRunner.SIG_READY,
        file_runner.FileRunner.SIG_BUSY,
        file_runner.FileRunner.SIG_OUTPUT,
        file_runner.FileRunner.SIG_READY,
    ]

    with open(resp_file, 'r') as f:
        resp = json.load(f)
    assert resp['status'] == 'failed'
    assert resp['error'] == 'prediction failed'

    stop_file = os.path.join(tmp_path, 'stop')
    Path(stop_file).touch()
    wait_for_process(p)


def test_file_runner_predict_canceled(tmp_path):
    signals = setup_signals()

    p = run_file_runner(tmp_path, 'sleep')

    openapi_file = os.path.join(tmp_path, 'openapi.json')
    wait_for_file(openapi_file)

    setup_result_file = os.path.join(tmp_path, 'setup_result.json')
    wait_for_file(setup_result_file)
    with open(setup_result_file) as f:
        setup_result = json.load(f)
    assert setup_result['status'] == 'succeeded'
    assert signals == [file_runner.FileRunner.SIG_READY]

    req_file = os.path.join(tmp_path, 'request-a.json')
    resp_file = os.path.join(tmp_path, 'response-a-00000.json')
    with open(req_file, 'w') as f:
        json.dump({'input': {'i': 60, 's': 'bar'}}, f)
    wait_for_file(req_file, exists=False)
    assert signals == [
        file_runner.FileRunner.SIG_READY,
        file_runner.FileRunner.SIG_BUSY,
    ]
    os.kill(p.pid, signal.SIGUSR1)
    wait_for_file(resp_file)
    assert signals == [
        file_runner.FileRunner.SIG_READY,
        file_runner.FileRunner.SIG_BUSY,
        file_runner.FileRunner.SIG_OUTPUT,
        file_runner.FileRunner.SIG_READY,
    ]

    with open(resp_file, 'r') as f:
        resp = json.load(f)
    assert resp['status'] == 'canceled'

    stop_file = os.path.join(tmp_path, 'stop')
    Path(stop_file).touch()
    wait_for_process(p)
