from typing import Optional

from cog import BasePredictor, Input


class Predictor(BasePredictor):
    def predict(
        self,
        s1: str,
        s2: Optional[str],
        s3: str = Input(),
        s4: Optional[str] = Input(),
        s5: str = Input(default=None),
        s6: str = Input(default=None, description='s6'),
        s7: str = Input(description='s7', default=None),
        s8: Optional[str] = Input(default=None),
    ) -> str:
        return f'{s1}:{s2}:{s3}:{s4}:{s5}:{s6}'
