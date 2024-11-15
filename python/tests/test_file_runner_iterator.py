import json
import os.path
import pathlib
import time
from typing import List, Optional

from cog.internal.file_runner import FileRunner
from tests.test_file_runner import file_runner, setup_signals


def test_file_runner_iterator(tmp_path):
    signals = setup_signals()
    p = file_runner(tmp_path, 'iterator')

    time.sleep(0.1)
    openapi_file = os.path.join(tmp_path, 'openapi.json')
    assert os.path.exists(openapi_file)

    setup_result_file = os.path.join(tmp_path, 'setup_result.json')
    assert os.path.exists(setup_result_file)
    with open(setup_result_file) as f:
        setup_result = json.load(f)
    assert setup_result['status'] == 'succeeded'
    assert signals == [FileRunner.SIG_READY]

    req_file = os.path.join(tmp_path, 'request-a.json')
    resp_file = os.path.join(tmp_path, 'response-a.json')
    with open(req_file, 'w') as f:
        json.dump({'input': {'i': 2, 's': 'bar'}}, f)
    assert os.path.exists(req_file)
    assert not os.path.exists(resp_file)
    time.sleep(0.1)
    assert not os.path.exists(req_file)
    assert signals == [FileRunner.SIG_READY, FileRunner.SIG_BUSY]
    time.sleep(2.1)
    assert os.path.exists(resp_file)
    assert signals == [
        FileRunner.SIG_READY,
        FileRunner.SIG_BUSY,
        FileRunner.SIG_OUTPUT,
        FileRunner.SIG_READY,
    ]

    with open(resp_file, 'r') as f:
        resp = json.load(f)
    assert resp['status'] == 'succeeded'
    assert resp['output'] == ['*bar-0*', '*bar-1*']

    stop_file = os.path.join(tmp_path, 'stop')
    pathlib.Path(stop_file).touch()
    assert p.poll() is None
    time.sleep(0.5)
    assert p.poll() == 0


def test_file_runner_iterator_webhook(tmp_path):
    signals = setup_signals()
    p = file_runner(tmp_path, 'iterator')

    time.sleep(0.1)
    openapi_file = os.path.join(tmp_path, 'openapi.json')
    assert os.path.exists(openapi_file)

    setup_result_file = os.path.join(tmp_path, 'setup_result.json')
    assert os.path.exists(setup_result_file)
    with open(setup_result_file) as f:
        setup_result = json.load(f)
    assert setup_result['status'] == 'succeeded'
    assert signals == [FileRunner.SIG_READY]

    def assert_output(status: str, output: Optional[List[str]]) -> None:
        with open(resp_file, 'r') as f:
            resp = json.load(f)
        assert resp['status'] == status
        assert resp.get('output') == output

    req_file = os.path.join(tmp_path, 'request-a.json')
    resp_file = os.path.join(tmp_path, 'response-a.json')
    with open(req_file, 'w') as f:
        json.dump({'input': {'i': 2, 's': 'bar'}, 'webhook': 'http://api'}, f)
    assert os.path.exists(req_file)
    assert not os.path.exists(resp_file)
    time.sleep(0.1)
    assert not os.path.exists(req_file)

    assert_output('starting', None)
    assert signals == [FileRunner.SIG_READY, FileRunner.SIG_BUSY, FileRunner.SIG_OUTPUT]

    time.sleep(1.1)
    assert_output('processing', ['*bar-0*'])
    assert signals == [
        FileRunner.SIG_READY,
        FileRunner.SIG_BUSY,
        FileRunner.SIG_OUTPUT,
        FileRunner.SIG_OUTPUT,
    ]

    time.sleep(1.1)
    assert_output('succeeded', ['*bar-0*', '*bar-1*'])
    assert signals == [
        FileRunner.SIG_READY,
        FileRunner.SIG_BUSY,
        FileRunner.SIG_OUTPUT,
        FileRunner.SIG_OUTPUT,
        FileRunner.SIG_OUTPUT,
        FileRunner.SIG_OUTPUT,
        FileRunner.SIG_READY,
    ]

    stop_file = os.path.join(tmp_path, 'stop')
    pathlib.Path(stop_file).touch()
    assert p.poll() is None
    time.sleep(0.5)
    assert p.poll() == 0
