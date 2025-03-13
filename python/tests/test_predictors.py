import importlib
import json
import os.path
import pkgutil
from typing import List

import pytest

from coglet import api, inspector, runner, schemas

# Test predictors in tests/schemas
# * run prediction with input/output fixture
# * produce same Open API schema as CogPy


def get_predictors() -> List[str]:
    schemas_dir = os.path.join(os.path.dirname(__file__), 'schemas')
    return [name for _, name, _ in pkgutil.iter_modules([schemas_dir])]


@pytest.mark.asyncio
@pytest.mark.parametrize('predictor', get_predictors())
async def test_predictor(predictor):
    module_name = f'tests.schemas.{predictor}'
    p = inspector.create_predictor(module_name, 'Predictor')
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


@pytest.mark.parametrize('predictor', get_predictors())
def test_schema(predictor):
    module_name = f'tests.schemas.{predictor}'
    class_name = 'Predictor'
    p = inspector.create_predictor(module_name, class_name)

    path = os.path.join(os.path.dirname(__file__), 'schemas', f'{predictor}.json')
    with open(path, 'r') as f:
        schema = json.load(f)

    # Compat: Cog handles secret differently
    if predictor == 'secrets':
        props = schema['components']['schemas']['Input']['properties']
        # Default Secret should be redacted
        props['s3']['default'] = '**********'
        # List[Secret] missing defaults
        props['ss']['default'] = ['**********', '**********']

    # Special case: Cog serializes Path() and Path("") to "."
    # Which is an invalid URI for web
    if predictor == 'paths':
        props = schema['components']['schemas']['Input']['properties']
        props['p4']['default'] = ''
        props['p5']['default'] = ''

    assert schemas.to_json_input(p) == schema['components']['schemas']['Input']
    assert schemas.to_json_output(p) == schema['components']['schemas']['Output']
    assert schemas.to_json_schema(p) == schema

    eq = api.Secret.__eq__
    if predictor == 'secrets':
        api.Secret.__eq__ = lambda self, other: type(other) is api.Secret

    assert schemas.from_json_input(schema) == p.inputs
    assert schemas.from_json_output(schema) == p.output
    assert schemas.from_json_schema(module_name, class_name, schema) == p

    if predictor == 'secrets':
        api.Secret.__eq__ = eq
