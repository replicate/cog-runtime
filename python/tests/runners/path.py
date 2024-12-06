import tempfile
import time

from cog import BasePredictor, Path


class Predictor(BasePredictor):
    def predict(self, p: Path) -> Path:
        with open(p, 'r') as f:
            print('reading input file')
            s = f.read()
        with tempfile.NamedTemporaryFile(mode='w', suffix='.txt', delete=False) as f:
            print('writing output file')
            f.write(f'*{s}*')
        time.sleep(0.1)
        return Path(f.name)
