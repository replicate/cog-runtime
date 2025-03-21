import importlib
import inspect
import re
import typing
from typing import Any, Callable, Dict, Optional

from coglet import adt, api, util


def _check_parent(child: type, parent: type) -> bool:
    return any(c is parent for c in inspect.getmro(child))


def _validate_setup(f: Callable) -> None:
    assert inspect.isfunction(f), f'not a function {f}'
    spec = inspect.getfullargspec(f)
    assert spec.args == ['self'] or spec.args == [
        'self',
        'weights',
    ], f'unexpected setup() arguments: {spec.args}'
    assert spec.varargs is None, 'setup() must not have *args'
    assert spec.varkw is None, 'setup() must not have **kwargs'
    assert spec.kwonlyargs == [], 'setup() must not have keyword-only args'
    assert spec.kwonlydefaults is None, 'setup() must not have keyword-only defaults'
    assert spec.annotations.get('return') is None, 'setup() must return None'


def _validate_predict(f: Callable) -> None:
    assert inspect.isfunction(f), f'not a function: {f}'
    spec = inspect.getfullargspec(f)
    assert spec.args[0] == 'self', "predict() must have 'self' first argument"
    assert spec.varargs is None, 'predict() must not have *args'
    assert spec.varkw is None, 'predict() must not have **kwargs'
    assert spec.kwonlyargs == [], 'predict() must not have keyword-only args'
    assert spec.kwonlydefaults is None, 'predict() must not have keyword-only defaults'
    assert spec.annotations.get('return') is not None, 'predict() must not return None'


def _validate_input(name: str, ft: adt.FieldType, cog_in: api.Input) -> None:
    defaults = []
    cog_t = ft.primitive
    if cog_in.default is not None:
        if ft.repetition is adt.Repetition.REPEATED:
            assert type(cog_in.default) is list, (
                f'default must be a list for input: {name}'
            )
            assert all(util.check_value(cog_t, v) for v in cog_in.default), (
                f'incompatible default for input: {name}]'
            )
            defaults = cog_in.default
        else:
            assert util.check_value(cog_t, cog_in.default), (
                f'incompatible default for input: {name}'
            )
            defaults = [cog_in.default]

    if cog_in.ge is not None or cog_in.le is not None:
        assert cog_t in adt.NUMERIC_TYPES, f'incompatible input type for ge/le: {name}'
        if cog_in.ge is not None:
            assert all(x >= cog_in.ge for x in defaults), (
                f'not all defaults >= {cog_in.ge} for input: {name}'
            )
        if cog_in.le is not None:
            assert all(x <= cog_in.le for x in defaults), (
                f'not all defaults <= {cog_in.ge} for input: {name}'
            )

    if cog_in.min_length is not None or cog_in.max_length is not None:
        assert cog_t is adt.PrimitiveType.STRING, (
            f'incompatible input type for min_length/max_length: {name}'
        )
        if cog_in.min_length is not None:
            assert all(len(x) >= cog_in.min_length for x in defaults), (
                f'not all defaults have len(x) >= {cog_in.min_length} for input: {name}'
            )
        if cog_in.max_length is not None:
            assert all(len(x) <= cog_in.max_length for x in defaults), (
                f'not all defaults have len(x) <= {cog_in.min_length} for input: {name}'
            )

    if cog_in.regex is not None:
        assert cog_t is adt.PrimitiveType.STRING, (
            f'incompatible input type for regex: {name}'
        )
        regex = re.compile(cog_in.regex)
        assert all(regex.match(x) for x in defaults), (
            f'not all defaults match regex for input: {name}'
        )

    if cog_in.choices is not None:
        assert cog_t in adt.CHOICE_TYPES, f'incompatible input type for choices: {name}'
        assert len(cog_in.choices) >= 2, f'choices must have >= 2 elements: {name}'
        assert cog_in.ge is None and cog_in.le is None, (
            f'choices and ge/le are mutually exclusive: {name}'
        )
        assert cog_in.min_length is None and cog_in.max_length is None, (
            f'choices and min_length/max_length are mutually exclusive: {name}'
        )
        assert all(adt.PYTHON_TO_COG.get(type(x)) is cog_t for x in cog_in.choices), (
            f'not all choices have the same type as input: {name}'
        )


def _input_adt(
    order: int, name: str, tpe: type, cog_in: Optional[api.Input]
) -> adt.Input:
    ft = util.get_field_type(tpe)
    if cog_in is None:
        return adt.Input(
            name=name,
            order=order,
            type=ft,
        )
    else:
        _validate_input(name, ft, cog_in)
        if cog_in.default is None:
            default = None
        else:
            if ft.repetition is adt.Repetition.REPEATED:
                default = [
                    util.normalize_value(ft.primitive, x) for x in cog_in.default
                ]
            else:
                default = util.normalize_value(ft.primitive, cog_in.default)
        return adt.Input(
            name=name,
            order=order,
            type=ft,
            default=default,
            description=cog_in.description,
            ge=float(cog_in.ge) if cog_in.ge is not None else None,
            le=float(cog_in.le) if cog_in.le is not None else None,
            min_length=cog_in.min_length,
            max_length=cog_in.max_length,
            regex=cog_in.regex,
            choices=cog_in.choices,
        )


def _output_adt(tpe: type) -> adt.Output:
    if inspect.isclass(tpe) and _check_parent(tpe, api.BaseModel):
        assert tpe.__name__ == 'Output', 'output type must be named Output'
        fields = {}
        for name, t in tpe.__annotations__.items():
            ft = util.get_field_type(t)
            assert ft.repetition is not adt.Repetition.REPEATED, (
                f'output field must not be list: {name}'
            )
            fields[name] = ft
        return adt.Output(kind=adt.Kind.OBJECT, fields=fields)

    kind = adt.CONTAINER_TO_COG.get(typing.get_origin(tpe)) or adt.Kind.SINGLE
    elem_t = tpe
    if kind is not adt.Kind.SINGLE:
        t_args = typing.get_args(tpe)
        assert len(t_args) == 1, 'repeated type must have one type argument'
        elem_t = t_args[0]
        if kind is adt.Kind.CONCAT_ITERATOR:
            assert elem_t is str, 'ConcatenateIterator must have str element'
    out_t = adt.PYTHON_TO_COG.get(elem_t)
    assert out_t is not None, f'unsupported output type {tpe}'
    return adt.Output(kind=kind, type=out_t)


def _predictor_adt(module_name: str, class_name: str, f: Callable) -> adt.Predictor:
    _validate_predict(f)
    spec = inspect.getfullargspec(f)
    names = spec.args[1:]
    defaults = spec.defaults if spec.defaults is not None else []
    cog_ins = [None] * (len(names) - len(defaults)) + list(defaults)
    inputs = {}
    for i, (name, cog_in) in enumerate(zip(names, cog_ins)):
        tpe = spec.annotations.get(name)
        assert tpe is not None, f'missing type annotation for {name}'
        inputs[name] = _input_adt(i, name, tpe, cog_in)
    output = _output_adt(spec.annotations['return'])
    return adt.Predictor(module_name, class_name, inputs, output)


# setup and predict might be decorated
def _unwrap(f: Callable) -> Callable:
    g = f
    while hasattr(g, '__closure__') and g.__closure__ is not None:
        cs = [
            c.cell_contents
            for c in g.__closure__
            if inspect.isfunction(c.cell_contents)
        ]
        assert len(cs) <= 1, f'unable to inspect function decorator: {f}'
        if len(cs) == 0:
            # No more functions in closure
            return g
        else:
            # 1 function in closure, keep digging
            g = cs[0]
    return g


def check_input(
    adt_ins: Dict[str, adt.Input], inputs: Dict[str, Any]
) -> Dict[str, Any]:
    kwargs: Dict[str, Any] = {}
    for name, value in inputs.items():
        assert name in adt_ins, f'unknown field: {name}'
        adt_in = adt_ins[name]
        cog_t = adt_in.type.primitive
        if adt_in.type.repetition is adt.Repetition.REPEATED:
            assert all(util.check_value(cog_t, v) for v in value), (
                f'incompatible value for field: {name}={value}'
            )
            value = [util.normalize_value(cog_t, v) for v in value]
        else:
            assert util.check_value(cog_t, value), (
                f'incompatible value for field: {name}={value}'
            )
            value = util.normalize_value(cog_t, value)
        kwargs[name] = value
    for name, adt_in in adt_ins.items():
        if name not in kwargs:
            # default=None is only allowed on `Optional[<type>]`
            if adt_in.type.repetition is not adt.Repetition.OPTIONAL:
                assert adt_in.default is not None or adt_in, (
                    f'missing default value for field: {name}'
                )
            kwargs[name] = adt_in.default

        values = (
            kwargs[name]
            if adt_in.type.repetition is adt.Repetition.REPEATED
            else [kwargs[name]]
        )
        v = kwargs[name]
        if adt_in.ge is not None:
            assert (x >= adt_in.ge for x in values), (
                f'validation failure: >= {adt_in.ge} for field: {name}={v}'
            )
        if adt_in.le is not None:
            assert (x <= adt_in.le for x in values), (
                f'validation failure: <= {adt_in.le} for field: {name}={v}'
            )
        if adt_in.min_length is not None:
            assert (len(x) >= adt_in.min_length for x in values), (
                f'validation failure: len(x) >= {adt_in.min_length} for field: {name}={v}'
            )
        if adt_in.max_length is not None:
            assert (len(x) <= adt_in.max_length for x in values), (
                f'validation failure: len(x) <= {adt_in.max_length} for field: {name}={v}'
            )
        if adt_in.regex is not None:
            p = re.compile(adt_in.regex)
            assert all(p.match(x) is not None for x in values), (
                f'validation failure: regex match for field: {name}={v}'
            )
        if adt_in.choices is not None:
            assert all(x in adt_in.choices for x in values), (
                f'validation failure: choices for field: {name}={v}'
            )
    return kwargs


def check_output(adt_out: adt.Output, output: Any) -> Any:
    if adt_out.kind is adt.Kind.SINGLE:
        assert adt_out.type is not None, 'missing output type'
        assert util.check_value(adt_out.type, output), f'incompatible output: {output}'
        return util.normalize_value(adt_out.type, output)
    elif adt_out.kind is adt.Kind.LIST:
        assert adt_out.type is not None, 'missing output type'
        assert type(output) is list, 'output is not list'
        for i, x in enumerate(output):
            assert util.check_value(adt_out.type, x), (
                f'incompatible output element: {x}'
            )
            output[i] = util.normalize_value(adt_out.type, x)
        return output
    elif adt_out.kind is adt.Kind.OBJECT:
        assert adt_out.fields is not None, 'missing output fields'
        for name, tpe in adt_out.fields.items():
            assert hasattr(output, name), f'missing output field: {name}'
            value = getattr(output, name)
            if value is None:
                assert tpe.repetition is adt.Repetition.OPTIONAL, (
                    f'missing value for output field: {name}'
                )
            else:
                assert util.check_value(tpe.primitive, value), (
                    f'incompatible output for field: {name}={value}'
                )
            setattr(output, name, util.normalize_value(tpe.primitive, value))
        return output


def create_predictor(module_name: str, class_name: str) -> adt.Predictor:
    module = importlib.import_module(module_name)
    fullname = f'{module_name}.{class_name}'
    assert hasattr(module, class_name), f'class not found: {fullname}'
    cls = getattr(module, class_name)
    assert inspect.isclass(cls), f'not a class: {fullname}'
    assert _check_parent(cls, api.BasePredictor), (
        f'predictor {fullname} does not inherit cog.BasePredictor'
    )

    assert hasattr(cls, 'setup'), f'setup method not found: {fullname}'
    assert hasattr(cls, 'predict'), f'predict method not found: {fullname}'
    _validate_setup(_unwrap(getattr(cls, 'setup')))
    return _predictor_adt(module_name, class_name, _unwrap(getattr(cls, 'predict')))


def get_test_inputs(
    cls: type[api.BasePredictor], inputs: Dict[str, adt.Input]
) -> Dict[str, Any]:
    if hasattr(cls, 'test_inputs'):
        test_inputs = getattr(cls, 'test_inputs')
    else:
        # Fall back to defaults if no test_inputs is not defined
        test_inputs = {}

    try:
        return check_input(inputs, test_inputs)
    except AssertionError as e:
        raise AssertionError(f'invalid test_inputs: {e}')
