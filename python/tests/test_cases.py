import pytest

from coglet import inspector, schemas

from .test_predictors import run_fixture


def test_repetition():
    module_name = 'tests.cases.repetition'
    class_name = 'Predictor'
    p = inspector.create_predictor(module_name, class_name)

    schema = schemas.to_json_schema(p)
    schema_in = schema['components']['schemas']['Input']

    # Fields with default=None or missing default are required
    # Unless if type hint is `Optional[T]`
    assert set(schema_in['required']) == {'rs', 'ls', 'rd0', 'rd1', 'ld0', 'ld1'}
    for name, prop in schema_in['properties'].items():
        # Only fields with default=X where X is not None are preserved
        if name in {'rd2', 'od2', 'ld2'}:
            assert 'default' in prop
        else:
            assert 'default' not in prop


@pytest.mark.asyncio
async def test_repetition_fixture():
    await run_fixture('tests.cases.repetition', 'Predictor')
