from cog import current_scope


def predict(s: str) -> str:
    print('predicting foo')
    token = current_scope().context['token']
    return f's={s}, token={token}'
