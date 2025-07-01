import os.path
import pkgutil
from typing import List

import pytest

from coglet import inspector, runner, scope
from tests.util import PythonVersionError


def get_predictors() -> List[str]:
    runners_dir = os.path.join(os.path.dirname(__file__), 'runners')
    return [name for _, name, _ in pkgutil.iter_modules([runners_dir])]


@pytest.mark.asyncio
@pytest.mark.parametrize('predictor', get_predictors())
async def test_test_inputs(predictor):
    module_name = f'tests.runners.{predictor}'

    entrypoint = 'Predictor'
    if predictor.startswith('function_'):
        entrypoint = 'predict'

    try:
        p = inspector.create_predictor(module_name, entrypoint)
        r = runner.Runner(p)

        # Some predictors calls current_scope() and requires ctx_pid
        scope.ctx_pid.set(predictor)
        assert await r.test()
    except PythonVersionError as e:
        pytest.skip(reason=str(e))
