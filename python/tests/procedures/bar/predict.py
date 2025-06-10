from cog import current_scope


def predict(i: int) -> str:
    print('predicting bar')
    token = current_scope().context['replicate_api_token']
    return f'i={i}, token={token}'
