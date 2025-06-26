import importlib
import os.path
import pkgutil
import re
from typing import List

import pytest

from coglet import inspector


def get_predictors() -> List[str]:
    errors_dir = os.path.join(os.path.dirname(__file__), 'errors')
    return [name for _, name, _ in pkgutil.iter_modules([errors_dir])]


def run_error(module_name: str, predictor_name: str) -> None:
    m = importlib.import_module(module_name)
    err_msg = getattr(m, 'ERROR')
    with pytest.raises(AssertionError, match=re.escape(err_msg)):
        inspector.create_predictor(module_name, predictor_name)


@pytest.mark.parametrize('predictor', get_predictors())
def test_predictor(predictor):
    module_name = f'tests.errors.{predictor}'
    run_error(module_name, 'Predictor')
