import json
import os.path
import pathlib
from typing import List, Optional

import pytest

from coglet import file_runner

from .test_file_runner import (
    run_file_runner,
    setup_signals,
    wait_for_file,
    wait_for_process,
)
from .test_file_runner_async import (
    async_wait_for_file,
    async_wait_for_process,
)


@pytest.mark.parametrize('predictor', ['iterator', 'concat_iterator', 'async_iterator'])
def test_file_runner_iterator(predictor, tmp_path):
    signals = setup_signals()
    p = run_file_runner(tmp_path, predictor)

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


@pytest.mark.parametrize('predictor', ['iterator', 'concat_iterator', 'async_iterator'])
def test_file_runner_iterator_webhook(predictor, tmp_path):
    signals = setup_signals()
    p = run_file_runner(tmp_path, predictor)

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


@pytest.mark.asyncio
async def test_file_runner_async_iterator_concurrency(tmp_path):
    signals = setup_signals()
    p = run_file_runner(tmp_path, 'async_iterator')

    openapi_file = os.path.join(tmp_path, 'openapi.json')
    await async_wait_for_file(openapi_file)

    setup_result_file = os.path.join(tmp_path, 'setup_result.json')
    await async_wait_for_file(setup_result_file)
    with open(setup_result_file) as f:
        setup_result = json.load(f)
    assert setup_result['status'] == 'succeeded'
    assert signals == [file_runner.FileRunner.SIG_READY]

    req_file_a = os.path.join(tmp_path, 'request-a.json')
    req_file_b = os.path.join(tmp_path, 'request-b.json')
    resp_file_a = os.path.join(tmp_path, 'response-a-00000.json')
    resp_file_b = os.path.join(tmp_path, 'response-b-00000.json')
    with open(req_file_a, 'w') as f:
        json.dump({'input': {'i': 2, 's': 'bar'}}, f)
    with open(req_file_b, 'w') as f:
        json.dump({'input': {'i': 2, 's': 'baz'}}, f)
    await async_wait_for_file(req_file_a, exists=False)
    await async_wait_for_file(req_file_b, exists=False)
    assert signals == [
        file_runner.FileRunner.SIG_READY,
        file_runner.FileRunner.SIG_BUSY,
    ]
    await async_wait_for_file(resp_file_a)
    await async_wait_for_file(resp_file_b)
    assert signals == [
        file_runner.FileRunner.SIG_READY,
        file_runner.FileRunner.SIG_BUSY,
        file_runner.FileRunner.SIG_OUTPUT,
        file_runner.FileRunner.SIG_OUTPUT,
        file_runner.FileRunner.SIG_READY,
    ]

    with open(resp_file_a, 'r') as f:
        resp_a = json.load(f)
    with open(resp_file_b, 'r') as f:
        resp_b = json.load(f)
    assert resp_a['status'] == 'succeeded'
    assert resp_a['output'] == ['*bar-0*', '*bar-1*']
    assert resp_b['status'] == 'succeeded'
    assert resp_b['output'] == ['*baz-0*', '*baz-1*']

    stop_file = os.path.join(tmp_path, 'stop')
    pathlib.Path(stop_file).touch()
    await async_wait_for_process(p)
