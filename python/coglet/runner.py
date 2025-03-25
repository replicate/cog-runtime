import importlib
import inspect
import os
import os.path
from typing import Any, AsyncGenerator, Dict

from coglet import adt, api, inspector


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

    async def test(self) -> Any:
        inputs = inspector.get_test_inputs(self.predictor, self.inputs)
        if self.is_iter():
            output = []
            async for o in self.predict_iter(inputs):
                output.append(o)
        else:
            output = await self.predict(inputs)
        return output

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
        kwargs = inspector.check_input(self.inputs, inputs)
        if self.is_async_predict:
            output = await self.predictor.predict(**kwargs)
        else:
            output = self.predictor.predict(**kwargs)
        return self.output.normalize(output)

    async def predict_iter(self, inputs: Dict[str, Any]) -> AsyncGenerator[Any, None]:
        assert self.is_iter(), 'predict does not return iterator, call predict instead'
        assert self.output.type is not None, 'missing output type'

        kwargs = inspector.check_input(self.inputs, inputs)
        if self.is_async_predict:
            async for x in self.predictor.predict(**kwargs):
                yield self.output.normalize(x)
        else:
            for x in self.predictor.predict(**kwargs):
                yield self.output.normalize(x)
