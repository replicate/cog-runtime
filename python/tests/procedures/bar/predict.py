import time

from cog import current_scope


def predict(i: int) -> str:
    print('predicting bar')
    time.sleep(1)
    token = current_scope().context['replicate_api_token']
    return f'i={i}, token={token}'
