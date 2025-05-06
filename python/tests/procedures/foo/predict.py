import os


def predict(s: str) -> str:
    print('predicting foo')
    token = os.environ.get('REPLICATE_API_TOKEN')
    return f's={s}, token={token}'
