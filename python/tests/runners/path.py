import tempfile

from cog import BasePredictor, Path


class Predictor(BasePredictor):
    def predict(self, p: Path) -> Path:
        with open(p, 'r') as f:
            s = f.read()
        with tempfile.NamedTemporaryFile(mode='w', suffix='.txt', delete=False) as f:
            f.write(f'*{s}*')
        return Path(f.name)
