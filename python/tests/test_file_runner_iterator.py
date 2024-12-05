import json
import os.path
import pathlib
from typing import List, Optional

from coglet import file_runner

from .test_file_runner import (
    run_file_runner,
    setup_signals,
    wait_for_file,
    wait_for_process,
)


def test_file_runner_iterator(tmp_path):
    signals = setup_signals()
    p = run_file_runner(tmp_path, 'iterator')

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
        json.dump({'input': {'i': 2, 's': 'bar'}}, f)
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
    assert resp['output'] == ['*bar-0*', '*bar-1*']

    stop_file = os.path.join(tmp_path, 'stop')
    pathlib.Path(stop_file).touch()
    wait_for_process(p)


def test_file_runner_iterator_webhook(tmp_path):
    signals = setup_signals()
    p = run_file_runner(tmp_path, 'iterator')

    openapi_file = os.path.join(tmp_path, 'openapi.json')
    wait_for_file(openapi_file)

    setup_result_file = os.path.join(tmp_path, 'setup_result.json')
    wait_for_file(setup_result_file)
    with open(setup_result_file) as f:
        setup_result = json.load(f)
    assert setup_result['status'] == 'succeeded'
    assert signals == [file_runner.FileRunner.SIG_READY]

    def assert_output(status: str, output: Optional[List[str]]) -> None:
        assert os.path.exists(resp_file)
        with open(resp_file, 'r') as f:
            resp = json.load(f)
        assert resp['status'] == status
        assert resp.get('output') == output

    req_file = os.path.join(tmp_path, 'request-a.json')
    resp_file = os.path.join(tmp_path, 'response-a-00000.json')
    with open(req_file, 'w') as f:
        json.dump({'input': {'i': 2, 's': 'bar'}, 'webhook': 'http://api'}, f)
    wait_for_file(req_file, exists=False)

    wait_for_file(resp_file)
    assert_output('starting', None)
    assert signals == [
        file_runner.FileRunner.SIG_READY,
        file_runner.FileRunner.SIG_BUSY,
        file_runner.FileRunner.SIG_OUTPUT,
    ]

    resp_file = os.path.join(tmp_path, 'response-a-00001.json')
    wait_for_file(resp_file)
    assert_output('processing', ['*bar-0*'])
    assert signals == [
        file_runner.FileRunner.SIG_READY,
        file_runner.FileRunner.SIG_BUSY,
        file_runner.FileRunner.SIG_OUTPUT,
        file_runner.FileRunner.SIG_OUTPUT,
    ]

    resp_file = os.path.join(tmp_path, 'response-a-00002.json')
    wait_for_file(resp_file)
    assert_output('processing', ['*bar-0*', '*bar-1*'])
    resp_file = os.path.join(tmp_path, 'response-a-00003.json')
    wait_for_file(resp_file)
    assert_output('succeeded', ['*bar-0*', '*bar-1*'])
    assert signals == [
        file_runner.FileRunner.SIG_READY,
        file_runner.FileRunner.SIG_BUSY,
        file_runner.FileRunner.SIG_OUTPUT,
        file_runner.FileRunner.SIG_OUTPUT,
        file_runner.FileRunner.SIG_OUTPUT,
        file_runner.FileRunner.SIG_OUTPUT,
        file_runner.FileRunner.SIG_READY,
    ]

    stop_file = os.path.join(tmp_path, 'stop')
    pathlib.Path(stop_file).touch()
    wait_for_process(p)
