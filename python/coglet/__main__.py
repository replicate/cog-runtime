import argparse
import asyncio
import importlib
import json
import logging
import os
import os.path
import sys
import time
from typing import Optional, Tuple

from coglet import file_runner, scope


def pre_setup(logger: logging.Logger, working_dir: str) -> Optional[Tuple[str, str]]:
    if os.environ.get('R8_TORCH_VERSION', '') != '':
        logger.info('eagerly importing torch')
        importlib.import_module('torch')

    # Cog server waits until user files become available and passes config to Python runner
    conf_file = os.path.join(working_dir, 'config.json')
    elapsed = 0.0
    timeout = 60.0
    while elapsed < timeout:
        if os.path.exists(conf_file):
            logger.info(f'config file found after {elapsed:.2f}s: {conf_file}')
            with open(conf_file, 'r') as f:
                conf = json.load(f)
                os.unlink(conf_file)
            module_name = conf['module_name']
            predictor_name = conf['predictor_name']

            # Add user venv to PYTHONPATH
            pv = f'python{sys.version_info.major}.{sys.version_info.minor}'
            venv = os.path.join('/', 'root', '.venv', 'lib', pv, 'site-packages')
            if venv is not None and venv not in sys.path and os.path.exists(venv):
                logger.info(f'adding venv to PYTHONPATH: {venv}')
                sys.path.append(venv)
                # In case the model forks Python interpreter
                os.environ['PYTHONPATH'] = ':'.join(sys.path)
            return module_name, predictor_name
        time.sleep(0.01)
        elapsed += 0.01

    logger.error(f'config file not found after {timeout:.2f}s: {conf_file}')
    return None


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument(
        '--working-dir', metavar='DIR', required=True, help='working directory'
    )
    parser.add_argument(
        '--procedure-mode',
        action='store_true',
        default=False,
        help='run in a style favorable to procedures',
    )

    logger = logging.getLogger('coglet')
    logger.setLevel(logging.INFO)
    if os.environ.get('LOG_LEVEL', '').strip().lower() == 'debug':
        logger.setLevel(logging.DEBUG)

    handler = logging.StreamHandler(sys.stdout)
    handler.setFormatter(
        logging.Formatter(
            '%(asctime)s\t%(levelname)s\t[%(name)s]\t%(filename)s:%(lineno)d\t%(message)s'
        )
    )
    logger.addHandler(handler)

    _stdout_write = sys.stdout.write
    _stderr_write = sys.stderr.write

    sys.stdout.write = scope.ctx_write(_stdout_write)  # type: ignore
    sys.stderr.write = scope.ctx_write(_stderr_write)  # type: ignore

    args = parser.parse_args()

    tup = pre_setup(logger, args.working_dir)
    if tup is None:
        return -1
    module_name, predictor_name = tup

    return asyncio.run(
        file_runner.FileRunner(
            logger=logger,
            working_dir=args.working_dir,
            module_name=module_name,
            predictor_name=predictor_name,
            procedure_mode=args.procedure_mode,
        ).start()
    )


if __name__ == '__main__':
    sys.exit(main())
