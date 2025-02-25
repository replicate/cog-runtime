import importlib
import inspect
import os
import os.path
import re
from typing import Any, AsyncGenerator, Dict

from coglet import adt, api, util


def _kwargs(adt_ins: Dict[str, adt.Input], inputs: Dict[str, Any]) -> Dict[str, Any]:
    kwargs: Dict[str, Any] = {}
    for name, value in inputs.items():
        assert name in adt_ins, f'unknown field: {name}'
        adt_in = adt_ins[name]
        cog_t = adt_in.type
        if adt_in.is_list:
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
            assert adt_in.default is not None, (
                f'missing default value for field: {name}'
            )
            kwargs[name] = adt_in.default

        values = kwargs[name] if adt_in.is_list else [kwargs[name]]
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


def _check_output(adt_out: adt.Output, output: Any) -> Any:
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
    elif adt_out.kind == adt.Kind.OBJECT:
        assert adt_out.fields is not None, 'missing output fields'
        for name, tpe in adt_out.fields.items():
            assert hasattr(output, name), f'missing output field: {name}'
            value = getattr(output, name)
            assert util.check_value(tpe, value), (
                f'incompatible output for field: {name}={value}'
            )
            setattr(output, name, util.normalize_value(tpe, value))
        return output


# Reflectively run a Cog predictor
# async by default and just run non-async setup/predict by blocking the caller
class Runner:
    def __init__(self, predictor: adt.Predictor):
        module = importlib.import_module(predictor.module_name)
        cls = getattr(module, predictor.class_name)
        self.inputs = predictor.inputs
        self.output = predictor.output
        self.predictor = cls()
        self.is_async_predict = inspect.iscoroutinefunction(
            self.predictor.predict
        ) or inspect.isasyncgenfunction(self.predictor.predict)

    async def setup(self) -> None:
        kwargs: Dict[str, Any] = {}
        if 'weights' in inspect.signature(self.predictor.setup).parameters:
            url = os.environ.get('COG_WEIGHTS')
            path = 'weights'
            if url:
                kwargs['weights'] = url
                self.predictor.setup(weights=url)
            elif os.path.exists(path):
                kwargs['weights'] = api.Path(path)
                self.predictor.setup(weights=api.Path(path))
            else:
                kwargs['weights'] = None
        if inspect.iscoroutinefunction(self.predictor.setup):
            return await self.predictor.setup(**kwargs)
        else:
            return self.predictor.setup(**kwargs)

    # functions can return regular values or generators, not both
    def is_iter(self) -> bool:
        return self.output.kind in {
            adt.Kind.ITERATOR,
            adt.Kind.CONCAT_ITERATOR,
        }

    async def predict(self, inputs: Dict[str, Any]) -> Any:
        assert not self.is_iter(), 'predict returns iterator, call predict_iter instead'
        kwargs = _kwargs(self.inputs, inputs)
        if self.is_async_predict:
            output = await self.predictor.predict(**kwargs)
        else:
            output = self.predictor.predict(**kwargs)
        return _check_output(self.output, output)

    async def predict_iter(self, inputs: Dict[str, Any]) -> AsyncGenerator[Any, None]:
        assert self.is_iter(), 'predict does not return iterator, call predict instead'
        assert self.output.type is not None, 'missing output type'

        kwargs = _kwargs(self.inputs, inputs)
        if self.is_async_predict:
            async for x in self.predictor.predict(**kwargs):
                assert util.check_value(self.output.type, x), (
                    f'incompatible output: {x}'
                )
                yield x
        else:
            for x in self.predictor.predict(**kwargs):
                assert util.check_value(self.output.type, x), (
                    f'incompatible output: {x}'
                )
                yield x
