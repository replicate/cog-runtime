import asyncio
import json
import os.path
import pathlib
import subprocess

import pytest

from coglet import file_runner

from .test_file_runner import run_file_runner, setup_signals


async def async_wait_for_file(path, exists: bool = True) -> None:
    while True:
        await asyncio.sleep(0.1)
        if os.path.exists(path) == exists:
            await asyncio.sleep(0.1)  # Wait for signal
            return


async def wait_for_process(p: subprocess.Popen, code: int = 0) -> None:
    while True:
        await asyncio.sleep(0.1)
        c = p.poll()
        if c is not None:
            assert c == code
            return


@pytest.mark.asyncio
async def test_file_runner_async(tmp_path):
    signals = setup_signals()

    env = os.environ.copy()
    env['SETUP_SLEEP'] = '1'
    p = run_file_runner(tmp_path, 'async_sleep', env=env)

    openapi_file = os.path.join(tmp_path, 'openapi.json')
    await async_wait_for_file(openapi_file)

    setup_result_file = os.path.join(tmp_path, 'setup_result.json')
    await async_wait_for_file(setup_result_file)
    with open(setup_result_file) as f:
        setup_result = json.load(f)
    assert setup_result['status'] == 'succeeded'
    assert signals == [file_runner.FileRunner.SIG_READY]

    req_file = os.path.join(tmp_path, 'request-a.json')
    resp_file = os.path.join(tmp_path, 'response-a-00000.json')
    with open(req_file, 'w') as f:
        json.dump({'input': {'i': 1, 's': 'bar'}}, f)
    await async_wait_for_file(req_file, exists=False)
    assert signals == [
        file_runner.FileRunner.SIG_READY,
        file_runner.FileRunner.SIG_BUSY,
    ]
    await async_wait_for_file(resp_file)
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
    pathlib.Path(stop_file).touch()
    await wait_for_process(p)


@pytest.mark.asyncio
async def test_file_runner_async_parallel(tmp_path):
    signals = setup_signals()
    p = run_file_runner(tmp_path, 'async_sleep')

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
        json.dump({'input': {'i': 1, 's': 'bar'}}, f)
    with open(req_file_b, 'w') as f:
        json.dump({'input': {'i': 1, 's': 'baz'}}, f)
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
    assert resp_a['output'] == '*bar*'
    assert resp_b['status'] == 'succeeded'
    assert resp_b['output'] == '*baz*'

    stop_file = os.path.join(tmp_path, 'stop')
    pathlib.Path(stop_file).touch()
    await wait_for_process(p)


@pytest.mark.asyncio
async def test_file_runner_async_cancel(tmp_path):
    signals = setup_signals()

    env = os.environ.copy()
    env['SETUP_SLEEP'] = '1'
    p = run_file_runner(tmp_path, 'async_sleep', env=env)

    openapi_file = os.path.join(tmp_path, 'openapi.json')
    await async_wait_for_file(openapi_file)

    setup_result_file = os.path.join(tmp_path, 'setup_result.json')
    await async_wait_for_file(setup_result_file)
    with open(setup_result_file) as f:
        setup_result = json.load(f)
    assert setup_result['status'] == 'succeeded'
    assert signals == [file_runner.FileRunner.SIG_READY]

    req_file = os.path.join(tmp_path, 'request-a.json')
    cancel_file = os.path.join(tmp_path, 'cancel-a')
    resp_file = os.path.join(tmp_path, 'response-a-00000.json')
    with open(req_file, 'w') as f:
        json.dump({'input': {'i': 60, 's': 'bar'}}, f)
    await async_wait_for_file(req_file, exists=False)
    assert signals == [
        file_runner.FileRunner.SIG_READY,
        file_runner.FileRunner.SIG_BUSY,
    ]

    pathlib.Path(cancel_file).touch()
    await async_wait_for_file(cancel_file, exists=False)

    await async_wait_for_file(resp_file)
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
    pathlib.Path(stop_file).touch()
    await wait_for_process(p)
