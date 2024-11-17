import asyncio
import json
import os.path
import pathlib

import pytest

from coglet.file_runner import FileRunner

from .test_file_runner import file_runner, setup_signals


@pytest.mark.asyncio
async def test_file_runner_async(tmp_path):
    signals = setup_signals()

    env = os.environ.copy()
    env['SETUP_SLEEP'] = '1'
    p = file_runner(tmp_path, 'async_sleep', env=env)

    await asyncio.sleep(0.1)
    openapi_file = os.path.join(tmp_path, 'openapi.json')
    assert os.path.exists(openapi_file)

    setup_result_file = os.path.join(tmp_path, 'setup_result.json')
    assert not os.path.exists(setup_result_file)
    await asyncio.sleep(1.1)
    assert os.path.exists(setup_result_file)
    with open(setup_result_file) as f:
        setup_result = json.load(f)
    assert setup_result['status'] == 'succeeded'
    assert signals == [FileRunner.SIG_READY]

    req_file = os.path.join(tmp_path, 'request-a.json')
    resp_file = os.path.join(tmp_path, 'response-a.json')
    with open(req_file, 'w') as f:
        json.dump({'input': {'i': 1, 's': 'bar'}}, f)
    assert os.path.exists(req_file)
    assert not os.path.exists(resp_file)
    await asyncio.sleep(0.1)
    assert not os.path.exists(req_file)
    assert signals == [FileRunner.SIG_READY, FileRunner.SIG_BUSY]
    await asyncio.sleep(1.1)
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
    assert resp['output'] == '*bar*'

    stop_file = os.path.join(tmp_path, 'stop')
    pathlib.Path(stop_file).touch()
    assert p.poll() is None
    await asyncio.sleep(0.5)
    assert p.poll() == 0


@pytest.mark.asyncio
async def test_file_runner_async_parallel(tmp_path):
    signals = setup_signals()
    p = file_runner(tmp_path, 'async_sleep')

    await asyncio.sleep(0.1)
    openapi_file = os.path.join(tmp_path, 'openapi.json')
    assert os.path.exists(openapi_file)

    setup_result_file = os.path.join(tmp_path, 'setup_result.json')
    assert os.path.exists(setup_result_file)
    with open(setup_result_file) as f:
        setup_result = json.load(f)
    assert setup_result['status'] == 'succeeded'
    assert signals == [FileRunner.SIG_READY]

    req_file_a = os.path.join(tmp_path, 'request-a.json')
    req_file_b = os.path.join(tmp_path, 'request-b.json')
    resp_file_a = os.path.join(tmp_path, 'response-a.json')
    resp_file_b = os.path.join(tmp_path, 'response-b.json')
    with open(req_file_a, 'w') as f:
        json.dump({'input': {'i': 1, 's': 'bar'}}, f)
    with open(req_file_b, 'w') as f:
        json.dump({'input': {'i': 1, 's': 'baz'}}, f)
    assert os.path.exists(req_file_a)
    assert os.path.exists(req_file_b)
    assert not os.path.exists(resp_file_a)
    assert not os.path.exists(resp_file_b)
    await asyncio.sleep(0.1)
    assert not os.path.exists(req_file_a)
    assert not os.path.exists(req_file_b)
    assert signals == [FileRunner.SIG_READY, FileRunner.SIG_BUSY]
    await asyncio.sleep(1.1)
    assert os.path.exists(resp_file_a)
    assert os.path.exists(resp_file_b)
    assert signals == [
        FileRunner.SIG_READY,
        FileRunner.SIG_BUSY,
        FileRunner.SIG_OUTPUT,
        FileRunner.SIG_OUTPUT,
        FileRunner.SIG_READY,
    ]

    with open(resp_file_a, 'r') as f:
        resp_a = json.load(f)
    with open(resp_file_b, 'r') as f:
        resp_b = json.load(f)
    assert resp_a['status'] == 'succeeded'
    assert resp_a['output'] == '*bar*'
    assert resp_b['status'] == 'succeeded'
    assert resp_b['output'] == '*baz*'

    stop_file = os.path.join(tmp_path, 'stop')
    pathlib.Path(stop_file).touch()
    assert p.poll() is None
    await asyncio.sleep(0.5)
    assert p.poll() == 0
