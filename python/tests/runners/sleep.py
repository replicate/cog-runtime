import os
import sys
import time

from cog import BasePredictor, current_scope
from cog.server.exceptions import CancelationException


class Predictor(BasePredictor):
    test_inputs = {'i': 0, 's': 'foo'}

    def setup(self) -> None:
        print('starting setup')
        i = int(os.environ.get('SETUP_SLEEP', '0'))
        for x in range(i):
            print(f'setup in progress {x + 1}/{i}')
            time.sleep(0.5)
        if int(os.environ.get('SETUP_FAILURE', '0')) == 1:
            print('setup failed')
            raise Exception('setup failed')
        if int(os.environ.get('SETUP_CRASH', '0')) == 1:
            print('setup crashed')
            sys.exit(1)
        print('completed setup')

    def predict(self, i: int, s: str) -> str:
        try:
            time.sleep(0.1)
            print('starting prediction')
            if i > 0:
                time.sleep(0.6)
            for x in range(i):
                print(f'prediction in progress {x + 1}/{i}')
                time.sleep(0.6)
            if int(os.environ.get('PREDICTION_FAILURE', '0')) == 1:
                print('prediction failed')
                raise Exception('prediction failed')
            if int(os.environ.get('PREDICTION_CRASH', '0')) == 1:
                print('prediction crashed')
                sys.exit(1)
            print('completed prediction')
            time.sleep(0.1)
            current_scope().record_metric('i', i)
            current_scope().record_metric('s_len', len(s))
            return f'*{s}*'
        except CancelationException as e:
            print('prediction canceled')
            raise e
