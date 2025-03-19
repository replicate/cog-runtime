import importlib
import json
import os.path
import pkgutil
from typing import List

import pytest

from coglet import inspector, runner, schemas

# Test predictors in tests/schemas
# * run prediction with input/output fixture
# * produce same Open API schema as CogPy


def get_predictors() -> List[str]:
    schemas_dir = os.path.join(os.path.dirname(__file__), 'schemas')
    return [name for _, name, _ in pkgutil.iter_modules([schemas_dir])]


async def run_fixture(module_name: str, class_name: str) -> None:
    p = inspector.create_predictor(module_name, class_name)
    r = runner.Runner(p)
    assert not r.predictor.setup_done
    await r.setup()
    assert r.predictor.setup_done

    m = importlib.import_module(module_name)
    fixture = getattr(m, 'FIXTURE')
    for inputs, output in fixture:
        if r.is_iter():
            result = [x async for x in r.predict_iter(inputs)]
            assert result == output
        else:
            result = await r.predict(inputs)
            assert result == output


@pytest.mark.asyncio
@pytest.mark.parametrize('predictor', get_predictors())
async def test_predictor(predictor):
    module_name = f'tests.schemas.{predictor}'
    await run_fixture(module_name, 'Predictor')


@pytest.mark.parametrize('predictor', get_predictors())
def test_schema(predictor):
    module_name = f'tests.schemas.{predictor}'
    class_name = 'Predictor'
    p = inspector.create_predictor(module_name, class_name)

    path = os.path.join(os.path.dirname(__file__), 'schemas', f'{predictor}.json')
    with open(path, 'r') as f:
        schema = json.load(f)

    # Compat: Cog handles secret differently
    if predictor == 'secret':
        props = schema['components']['schemas']['Input']['properties']
        # Default Secret should be redacted
        props['s3']['default'] = '**********'
        # List[Secret] missing defaults
        props['ss']['default'] = ['**********', '**********']

    assert schemas.to_json_input(p) == schema['components']['schemas']['Input']
    assert schemas.to_json_output(p) == schema['components']['schemas']['Output']
    assert schemas.to_json_schema(p) == schema
