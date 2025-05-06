import os


def predict(i: int) -> str:
    print('predicting bar')
    token = os.environ.get('REPLICATE_API_TOKEN')
    return f'i={i}, token={token}'
