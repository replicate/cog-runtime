import pathlib
import sys
import warnings

# NOTE: The compatibility import provided in `./_compat/cog.py` **SHOULD NOT** be in
# PYTHONPATH until `coglet` is imported. This prevents `coglet` from interfering with
# normal usage of `cog` within a given python environment.
warnings.warn(
    (
        'coglet/_compat/ is being added to the front of sys.path '
        "for 'cog' import compatibility"
    ),
    category=ImportWarning,
    stacklevel=2,
)
sys.path.insert(0, str(pathlib.Path(__file__).absolute().parent / '_compat'))
