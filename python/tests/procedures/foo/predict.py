import time

from cog import current_scope


async def predict(s: str) -> str:
    print('predicting foo')
    time.sleep(1)
    token = current_scope().context['replicate_api_token']
    return f's={s}, token={token}'
