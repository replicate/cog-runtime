import asyncio
import os

from cog import BasePredictor


class Predictor(BasePredictor):
    async def setup(self) -> None:
        print('starting async setup')
        i = int(os.environ.get('SETUP_SLEEP', '0'))
        for x in range(i):
            print(f'setup in progress {x+1}/{i}')
            await asyncio.sleep(1)
        print('completed async setup')

    async def predict(self, i: int, s: str) -> str:
        print('starting async prediction')
        for x in range(i):
            print(f'prediction in progress {x+1}/{i}')
            await asyncio.sleep(1)
        print('completed async prediction')
        return f'*{s}*'
