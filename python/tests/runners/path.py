import tempfile

from cog import BasePredictor, Path


class Predictor(BasePredictor):
    def predict(self, s: str) -> Path:
        with tempfile.NamedTemporaryFile(mode='w', suffix='.txt', delete=False) as f:
            f.write(f'*{s}*')
        return Path(f.name)
