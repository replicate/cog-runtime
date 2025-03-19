from dataclasses import dataclass

from cog import BasePredictor


@dataclass(frozen=True)
class Address:
    street: str
    zip: str


@dataclass(frozen=True)
class Account:
    id: int
    name: str
    address: Address


class Predictor(BasePredictor):
    test_inputs = {
        'a': Account(id=0, name='John', address=Address(street='Smith St', zip='12345'))
    }

    def predict(self, a: Account) -> str:
        return str(a)
