"""Unit tests for the Input() function and InputSpec dataclass."""

import pytest

from coglet.api import FieldInfo, Input


class TestInputSpec:
    """Test the InputSpec dataclass."""

    def test_default_values(self):
        """Test InputSpec with all default values."""
        spec = FieldInfo()
        assert spec.default is None
        assert spec.description is None
        assert spec.ge is None
        assert spec.le is None
        assert spec.min_length is None
        assert spec.max_length is None
        assert spec.regex is None
        assert spec.choices is None
        assert spec.deprecated is None

    def test_custom_values(self):
        """Test InputSpec with custom values."""
        spec = FieldInfo(
            default='test',
            description='A test field',
            ge=1,
            le=10,
            min_length=2,
            max_length=20,
            regex=r'\d+',
            choices=['a', 'b', 'c'],
            deprecated=True,
        )
        assert spec.default == 'test'
        assert spec.description == 'A test field'
        assert spec.ge == 1
        assert spec.le == 10
        assert spec.min_length == 2
        assert spec.max_length == 20
        assert spec.regex == r'\d+'
        assert spec.choices == ['a', 'b', 'c']
        assert spec.deprecated is True

    def test_frozen_dataclass(self):
        """Test that InputSpec is frozen (immutable)."""
        spec = FieldInfo(default='test')
        with pytest.raises(AttributeError):
            spec.default = 'changed'


class TestInputFunction:
    """Test the Input() function."""

    def test_input_no_args(self):
        """Test Input() with no arguments."""
        result = Input()
        assert isinstance(result, FieldInfo)
        assert result.default is None
        assert result.description is None

    def test_input_default_only(self):
        """Test Input() with default value only."""
        result = Input('test_default')
        assert isinstance(result, FieldInfo)
        assert result.default == 'test_default'
        assert result.description is None

    def test_input_all_params(self):
        """Test Input() with all parameters."""
        result = Input(
            default=42,
            description='Test field',
            ge=0,
            le=100,
            min_length=1,
            max_length=50,
            regex=r'^\d+$',
            choices=[1, 2, 3],
            deprecated=False,
        )
        assert isinstance(result, FieldInfo)
        assert result.default == 42
        assert result.description == 'Test field'
        assert result.ge == 0
        assert result.le == 100
        assert result.min_length == 1
        assert result.max_length == 50
        assert result.regex == r'^\d+$'
        assert result.choices == [1, 2, 3]
        assert result.deprecated is False

    def test_input_keyword_only(self):
        """Test that non-default parameters are keyword-only."""
        # This should work
        result = Input(default='test', description='desc')
        assert result.default == 'test'
        assert result.description == 'desc'

        # This would be a syntax error if description wasn't keyword-only
        # Input("test", "desc")  # Would fail if not keyword-only

    def test_input_return_type_is_any(self):
        """Test that Input() has return type Any for type checkers."""
        # This is more of a static analysis test, but we can verify
        # that the function returns InputSpec at runtime
        result = Input(description='test')
        assert isinstance(result, FieldInfo)


class TestInputUsagePatterns:
    """Test common usage patterns of Input()."""

    def test_basic_field(self):
        """Test basic field definition pattern."""
        # Simulate: name: str = Input()
        field_spec = Input()
        assert isinstance(field_spec, FieldInfo)
        assert field_spec.default is None

    def test_field_with_default(self):
        """Test field with default value."""
        # Simulate: name: str = Input(default="foo")
        field_spec = Input(default='foo')
        assert field_spec.default == 'foo'

    def test_field_with_description(self):
        """Test field with description."""
        # Simulate: name: str = Input(description="User's name")
        field_spec = Input(description="User's name")
        assert field_spec.description == "User's name"

    def test_numeric_constraints(self):
        """Test numeric constraint fields."""
        # Simulate: age: int = Input(ge=0, le=120)
        field_spec = Input(ge=0, le=120)
        assert field_spec.ge == 0
        assert field_spec.le == 120

    def test_string_constraints(self):
        """Test string constraint fields."""
        # Simulate: name: str = Input(min_length=1, max_length=50)
        field_spec = Input(min_length=1, max_length=50)
        assert field_spec.min_length == 1
        assert field_spec.max_length == 50

    def test_regex_constraint(self):
        """Test regex constraint field."""
        # Simulate: email: str = Input(regex=r'.*@.*')
        field_spec = Input(regex=r'.*@.*')
        assert field_spec.regex == r'.*@.*'

    def test_choices_constraint(self):
        """Test choices constraint field."""
        # Simulate: color: str = Input(choices=['red', 'green', 'blue'])
        field_spec = Input(choices=['red', 'green', 'blue'])
        assert field_spec.choices == ['red', 'green', 'blue']

    def test_deprecated_field(self):
        """Test deprecated field."""
        # Simulate: old_param: str = Input(deprecated=True)
        field_spec = Input(deprecated=True)
        assert field_spec.deprecated is True

    def test_complex_field(self):
        """Test field with multiple constraints."""
        # Simulate: score: int = Input(
        #     default=50,
        #     description="Score between 0 and 100",
        #     ge=0,
        #     le=100
        # )
        field_spec = Input(
            default=50, description='Score between 0 and 100', ge=0, le=100
        )
        assert field_spec.default == 50
        assert field_spec.description == 'Score between 0 and 100'
        assert field_spec.ge == 0
        assert field_spec.le == 100


class TestTypeCompatibility:
    """Test that Input() works with type annotations."""

    def test_string_annotation(self):
        """Test Input() used with string type annotation."""
        # This simulates: name: str = Input()
        # The type checker should see this as valid
        field_spec = Input()
        assert isinstance(field_spec, FieldInfo)

    def test_optional_annotation(self):
        """Test Input() used with Optional type annotation."""
        # This simulates: name: Optional[str] = Input()
        field_spec = Input()
        assert isinstance(field_spec, FieldInfo)

    def test_list_annotation(self):
        """Test Input() used with List type annotation."""
        # This simulates: names: List[str] = Input()
        field_spec = Input()
        assert isinstance(field_spec, FieldInfo)

    def test_with_defaults_various_types(self):
        """Test Input() with defaults of various types."""
        str_field = Input(default='string')
        int_field = Input(default=42)
        float_field = Input(default=3.14)
        bool_field = Input(default=True)
        list_field = Input(default=[1, 2, 3])
        none_field = Input(default=None)

        assert str_field.default == 'string'
        assert int_field.default == 42
        assert float_field.default == 3.14
        assert bool_field.default is True
        assert list_field.default == [1, 2, 3]
        assert none_field.default is None
