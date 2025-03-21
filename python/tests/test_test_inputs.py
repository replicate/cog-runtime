import os.path
import pkgutil
from typing import List

import pytest

from coglet import inspector, runner, scope


def get_predictors() -> List[str]:
    schemas_dir = os.path.join(os.path.dirname(__file__), 'runners')
    return [name for _, name, _ in pkgutil.iter_modules([schemas_dir])]


@pytest.mark.asyncio
@pytest.mark.parametrize('predictor', get_predictors())
async def test_test_inputs(predictor):
    module_name = f'tests.runners.{predictor}'
    p = inspector.create_predictor(module_name, 'Predictor')
    r = runner.Runner(p)

    # Some predictors calls current_scope() and requires ctx_pid
    scope.ctx_pid.set(predictor)
    assert await r.test()
