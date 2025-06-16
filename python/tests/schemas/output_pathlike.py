import os

from cog import BasePredictor, Path


class MyPath(os.PathLike):
    def __init__(self, path: str) -> None:
        # Store path components internally
        self._path = path

    def __fspath__(self) -> str:
        # Build and return a string path
        return self._path

    def __repr__(self) -> str:
        return f'MyPath({self._path!r})'


FIXTURE = [
    ({'x': 'foo.txt'}, Path('/tmp/foo.txt')),
    ({'x': Path('bar.txt')}, Path('/tmp/bar.txt')),
]


class Predictor(BasePredictor):
    setup_done = False

    def setup(self) -> None:
        self.setup_done = True

    def predict(self, x: Path) -> MyPath:
        return MyPath(f'/tmp/{x}')
