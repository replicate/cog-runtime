import asyncio
import contextvars
import json
import logging
import os
import re
import signal
import sys
from typing import Any, Dict, Optional

from coglet import inspector, runner, schemas, util


class FileRunner:
    CANCEL_RE = re.compile(r'^cancel-(?P<pid>\S+).json$')
    REQUEST_RE = re.compile(r'^request-(?P<pid>\S+).json$')
    RESPONSE_FMT = 'response-{pid}.json'

    # Signal parent to scan output
    SIG_OUTPUT = signal.SIGHUP

    # Signal ready or busy status
    SIG_READY = signal.SIGUSR1
    SIG_BUSY = signal.SIGUSR2

    def __init__(
        self,
        *,
        logger: logging.Logger,
        working_dir: str,
        module_name: str,
        class_name: str,
        ctx_pid: contextvars.ContextVar[Optional[str]],
    ):
        self.logger = logger
        self.working_dir = working_dir
        self.module_name = module_name
        self.class_name = class_name
        self.runner: Optional[runner.Runner] = None
        self.ctx_pid = ctx_pid
        self.isatty = sys.stdout.isatty()

    async def start(self) -> int:
        self.logger.info(
            'starting file runner: working_dir=%s, predict=%s.%s',
            self.working_dir,
            self.module_name,
            self.class_name,
        )

        os.makedirs(self.working_dir, exist_ok=True)
        setup_result_file = os.path.join(self.working_dir, 'setup_result.json')
        stop_file = os.path.join(self.working_dir, 'stop')
        openapi_file = os.path.join(self.working_dir, 'openapi.json')
        if os.path.exists(setup_result_file):
            os.unlink(setup_result_file)
        if os.path.exists(stop_file):
            os.unlink(stop_file)
        if os.path.exists(openapi_file):
            os.unlink(openapi_file)

        # FIXME: eager import torch, etc. and wait for user tarball
        self.logger.info('setup started')
        setup_result: Dict[str, Any] = {'started_at': util.now_iso()}
        try:
            p = inspector.create_predictor(self.module_name, self.class_name)
            # FIXME: reuse frozen schema?
            with open(openapi_file, 'w') as f:
                schema = schemas.to_json_schema(p)
                json.dump(schema, f)
            self.runner = runner.Runner(p)

            await self.runner.setup()
            self.logger.info('setup completed')
            setup_result['status'] = 'succeeded'
        except Exception as e:
            self.logger.error('setup failed: %s', e)
            setup_result['status'] = 'failed'
        finally:
            setup_result['completed_at'] = util.now_iso()
        with open(setup_result_file, 'w') as f:
            json.dump(setup_result, f)
        if setup_result['status'] == 'failed':
            return 1

        ready = True
        self._signal(FileRunner.SIG_READY)

        pending: Dict[str, asyncio.Task[None]] = {}
        while True:
            if len(pending) == 0 and not ready:
                ready = True
                self._signal(FileRunner.SIG_READY)

            if os.path.exists(stop_file):
                self.logger.info('stopping file runner')
                tasks = []
                for pid, task in pending.items():
                    if not task.done():
                        task.cancel()
                        tasks.append(task)
                        self.logger.info('prediction cancelled: id=%s', pid)
                await asyncio.gather(*tasks)
                return 0

            for entry in os.listdir(self.working_dir):
                m = self.CANCEL_RE.match(entry)
                if m is not None:
                    os.unlink(os.path.join(self.working_dir, entry))
                    pid = m.group('pid')
                    t = pending.get(pid)
                    if t is None:
                        self.logger.warning(
                            'failed to cancel non-existing prediction: id=%s', pid
                        )
                    elif t.done():
                        self.logger.warning(
                            'failed to cancel completed prediction: id=%s', pid
                        )
                    else:
                        t.cancel()
                        self.logger.info('canceling prediction: id=%s', pid)
                    continue

                m = self.REQUEST_RE.match(entry)
                if m is None:
                    continue
                if ready:
                    ready = False
                    self._signal(FileRunner.SIG_BUSY)
                pid = m.group('pid')
                req_path = os.path.join(self.working_dir, entry)
                with open(req_path, 'r') as f:
                    req = json.load(f)
                os.unlink(req_path)

                pending[pid] = asyncio.create_task(self._predict(pid, req))
                self.logger.info('prediction started: id=%s', pid)

            done_pids = []
            for pid, task in pending.items():
                if not task.done():
                    continue
                done_pids.append(pid)
            for pid in done_pids:
                del pending[pid]

            await asyncio.sleep(0.1)

    async def _predict(self, pid: str, req: Dict[str, Any]) -> None:
        assert self.runner is not None
        self.ctx_pid.set(pid)
        resp: Dict[str, Any] = {
            'started_at': util.now_iso(),
            'status': 'starting',
        }
        # Write partial response, e.g. starting, processing, if webhook is set
        is_async = 'webhook' in req
        try:
            if is_async:
                self._respond(pid, resp)

            if self.runner.is_iter():
                resp['output'] = []
                async for o in self.runner.predict_iter(req['input']):
                    resp['output'].append(o)
                    resp['status'] = 'processing'
                    if is_async:
                        self._respond(pid, resp)
            else:
                resp['output'] = await self.runner.predict(req['input'])
            resp['status'] = 'succeeded'
            self.logger.info('prediction completed: id=%s', pid)
        except asyncio.CancelledError:
            resp['status'] = 'canceled'
            self.logger.error('prediction canceled: id=%s', pid)
        except Exception as e:
            resp['error'] = str(e)
            resp['status'] = 'failed'
            self.logger.error('prediction failed: id=%s %s', pid, e)
        finally:
            resp['completed_at'] = util.now_iso()
        self._respond(pid, resp)

    def _respond(
        self,
        pid: str,
        resp: Dict[str, Any],
    ) -> None:
        resp_path = os.path.join(self.working_dir, self.RESPONSE_FMT.format(pid=pid))
        with open(resp_path, 'w') as f:
            json.dump(resp, f)
        self._signal(FileRunner.SIG_OUTPUT)

    def _signal(self, signum: int) -> None:
        if not self.isatty:
            os.kill(os.getppid(), signum)