import sys

# Only skip if we're running under pytest
if 'pytest' in sys.modules:
    import pytest

    pytest.skip(
        'This module contains intentionally bad mutable defaults for Go testing',
        allow_module_level=True,
    )

from cog import BasePredictor, Input


class Predictor(BasePredictor):
    test_inputs = {'items': [4, 5, 6]}

    def predict(self, items: list = Input(default=[1, 2, 3])) -> str:
        return f'items: {items}'
