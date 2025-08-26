from dataclasses import is_dataclass
from typing import Optional

import pytest

from coglet.api import _BaseModelPyodide


def test_basemodel_pyodide_creates_dataclass():
    """Test that _BaseModelPyodide converts subclasses to dataclasses."""

    class Output(_BaseModelPyodide):
        text: str
        count: int
        metadata: Optional[str] = None

    # Core fix: Should be recognized as a dataclass by pyodide
    assert is_dataclass(Output)

    # Should work like a normal dataclass
    instance = Output(text='hello', count=5)
    assert instance.text == 'hello'
    assert instance.count == 5
    assert instance.metadata is None

    instance2 = Output(text='world', count=10, metadata='test')
    assert instance2.metadata == 'test'


def test_basemodel_pyodide_with_frozen():
    """Test that frozen=True dataclass option works correctly."""

    class FrozenOutput(_BaseModelPyodide, frozen=True):
        result: str
        score: int

    assert is_dataclass(FrozenOutput)

    instance = FrozenOutput(result='success', score=100)
    assert instance.result == 'success'
    assert instance.score == 100

    # Should not be able to modify frozen instance
    with pytest.raises(AttributeError):
        instance.result = 'modified'


def test_basemodel_pyodide_already_dataclass():
    """Test that classes already decorated as dataclasses are not double-converted."""

    from dataclasses import dataclass

    @dataclass
    class AlreadyDataclass(_BaseModelPyodide):
        name: str
        value: int

    # Should still be a dataclass and work normally
    assert is_dataclass(AlreadyDataclass)

    instance = AlreadyDataclass(name='test', value=42)
    assert instance.name == 'test'
    assert instance.value == 42
