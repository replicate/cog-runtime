import asyncio
import json
import logging
import os
import pathlib
import re
import signal
import tempfile
import urllib.request
from dataclasses import dataclass
from typing import Any, Dict, Optional

from coglet import api, inspector, runner, schemas, scope, util


@dataclass(frozen=True)
class Config:
    module_name: str
    predictor_name: str
    max_concurrency: int


class FileRunner:
    CANCEL_RE = re.compile(r'^cancel-(?P<pid>\S+)$')
    REQUEST_RE = re.compile(r'^request-(?P<pid>\S+).json$')
    RESPONSE_FMT = 'response-{pid}-{epoch:05d}.json'

    # IPC status updates to Go server
    IPC_READY = 'READY'
    IPC_BUSY = 'BUSY'
    IPC_OUTPUT = 'OUTPUT'

    def __init__(
        self,
        *,
        logger: logging.Logger,
        ipc_url: str,
        working_dir: str,
        config: Config,
    ):
        self.logger = logger
        self.ipc_url = ipc_url
        self.working_dir = working_dir
        self.config = config
        self.runner: Optional[runner.Runner] = None

    async def start(self) -> int:
        self.logger.info(
            'starting file runner: working_dir=%s, module=%s, predictor=%s, max_concurrency=%d',
            self.working_dir,
            self.config.module_name,
            self.config.predictor_name,
            self.config.max_concurrency,
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

        self.logger.info('setup started')
        setup_result: Dict[str, Any] = {'started_at': util.now_iso()}
        try:
            p = inspector.create_predictor(
                self.config.module_name, self.config.predictor_name
            )
            with open(openapi_file, 'w') as f:
                schema = schemas.to_json_schema(p)
                json.dump(schema, f)
            self.runner = runner.Runner(p)

            await self.runner.setup()
            self.logger.info('setup completed')
            setup_result['status'] = 'succeeded'
        except Exception as e:
            self.logger.exception('setup failed: %s', e)
            setup_result['status'] = 'failed'
        setup_result['completed_at'] = util.now_iso()
        with open(setup_result_file, 'w') as f:
            json.dump(setup_result, f)
        if setup_result['status'] == 'failed':
            return 1

        def _noop_handler(_signum, _) -> None:
            pass

        def _cancel_handler(signum, _) -> None:
            # ctx_pid is set when we are inside a prediction
            if signum == signal.SIGUSR1 and scope.ctx_pid.get() is not None:
                raise api.CancelationException()

        if self.runner is not None and self.runner.is_async_predict:
            # Async predict, use files to cancel
            pathlib.Path(os.path.join(self.working_dir, 'async_predict')).touch()
            signal.signal(signal.SIGUSR1, _noop_handler)
        else:
            # Blocking predict, use SIGUSR1 to cancel
            signal.signal(signal.SIGUSR1, _cancel_handler)

        ready = True
        self._send_ipc(FileRunner.IPC_READY)

        pending: Dict[str, asyncio.Task[None]] = {}
        while True:
            if len(pending) < self.config.max_concurrency and not ready:
                ready = True
                self._send_ipc(FileRunner.IPC_READY)

            if os.path.exists(stop_file):
                self.logger.info('stopping file runner')
                tasks = []
                for pid, task in pending.items():
                    if not task.done():
                        task.cancel()
                        tasks.append(task)
                        self.logger.info('prediction canceled: id=%s', pid)
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
                    self._send_ipc(FileRunner.IPC_BUSY)
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

        resp: Dict[str, Any] = {
            'status': 'starting',
        }
        context_dict = {}
        if 'context' in req:
            context = req['context']
            if context is not None:
                if 'procedure_source_url' in context:
                    context_dict['procedure_source_url'] = context[
                        'procedure_source_url'
                    ]
                if 'replicate_api_token' in context:
                    context_dict['replicate_api_token'] = context['replicate_api_token']
        scope.contexts[pid] = context_dict
        # Write partial response, e.g. starting, processing, if webhook is set
        is_async = 'webhook' in req
        epoch = 0
        try:
            if is_async:
                self._respond(pid, epoch, resp)
                epoch += 1

            req_in = req['input']
            for k, v in req_in.items():
                req_in[k] = self.runner.inputs[k].type.json_decode(v)

            if self.runner.is_iter():
                resp['output'] = []
                resp['status'] = 'processing'
                scope.ctx_pid.set(pid)
                async for o in self.runner.predict_iter(req_in):
                    # Test JSON serialization in case of invalid output
                    o = self.runner.output.json_encode(o)
                    json.dumps(o, default=util.output_json)

                    resp['output'].append(o)
                    if is_async:
                        self._respond(pid, epoch, resp)
                        epoch += 1
            else:
                scope.ctx_pid.set(pid)
                o = await self.runner.predict(req_in)
                o = self.runner.output.json_encode(o)
                # Test JSON serialization in case of invalid output
                json.dumps(o, default=util.output_json)
                resp['output'] = o
            scope.ctx_pid.set(None)

            resp['status'] = 'succeeded'
            self.logger.info('prediction completed: id=%s', pid)
        except api.CancelationException:
            resp['status'] = 'canceled'
            scope.ctx_pid.set(None)
            self.logger.error('prediction canceled: id=%s', pid)
        except asyncio.CancelledError:
            resp['status'] = 'canceled'
            scope.ctx_pid.set(None)
            self.logger.error('prediction canceled: id=%s', pid)
        except Exception as e:
            resp['error'] = str(e)
            resp['status'] = 'failed'
            scope.ctx_pid.set(None)
            self.logger.exception('prediction failed: id=%s %s', pid, e)
        finally:
            resp['completed_at'] = util.now_iso()
        self._respond(pid, epoch, resp)
        if pid in scope.metrics:
            scope.metrics.pop(pid)
        if pid in scope.ctx_write_buf:
            scope.ctx_write_buf.pop(pid)
        if pid in scope.contexts:
            scope.contexts.pop(pid)
        epoch += 1

    def _respond(
        self,
        pid: str,
        epoch: int,
        resp: Dict[str, Any],
    ) -> None:
        m = scope.metrics.get(pid)
        if m is not None:
            if 'metrics' not in resp:
                resp['metrics'] = {}
            resp['metrics'].update(m)

        # Write to a temp file and atomically rename to avoid Go server picking up an incomplete file
        (_, temp_path) = tempfile.mkstemp(
            suffix='.json', prefix=f'response-{pid}-{epoch}'
        )
        with open(temp_path, 'w') as f:
            json.dump(resp, f, default=util.output_json)
        resp_path = os.path.join(
            self.working_dir, self.RESPONSE_FMT.format(pid=pid, epoch=epoch)
        )
        os.rename(temp_path, resp_path)

        self._send_ipc(FileRunner.IPC_OUTPUT)

    def _send_ipc(self, status: str) -> None:
        try:
            payload = {
                'pid': os.getpid(),
                'status': status,
                'working_dir': self.working_dir,
            }
            data = json.dumps(payload).encode('utf-8')
            urllib.request.urlopen(self.ipc_url, data=data).read()
        except Exception as e:
            self.logger.exception('IPC failed: %s', e)
