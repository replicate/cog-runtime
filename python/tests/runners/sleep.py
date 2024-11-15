import os
import sys
import time

from cog import BasePredictor


class Predictor(BasePredictor):
    def setup(self) -> None:
        print('starting setup')
        i = int(os.environ.get('SETUP_SLEEP', '0'))
        for x in range(i):
            print(f'setup in progress {x+1}/{i}')
            time.sleep(1)
        if int(os.environ.get('SETUP_FAILURE', '0')) == 1:
            print('setup failed')
            raise Exception('setup failed')
        if int(os.environ.get('SETUP_CRASH', '0')) == 1:
            print('setup crashed')
            sys.exit(1)
        print('completed setup')

    def predict(self, i: int, s: str) -> str:
        print('starting prediction')
        for x in range(i):
            print(f'prediction in progress {x+1}/{i}')
            time.sleep(1)
        if int(os.environ.get('PREDICTION_FAILURE', '0')) == 1:
            print('prediction failed')
            raise Exception('prediction failed')
        if int(os.environ.get('PREDICTION_CRASH', '0')) == 1:
            print('prediction crashed')
            sys.exit(1)
        print('completed prediction')
        return f'*{s}*'
