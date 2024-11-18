import pathlib
import sys
import warnings

warnings.warn(
    (
        'coglet/_compat/ is being added to the front of sys.path '
        "for 'cog' import compatibility"
    ),
    category=ImportWarning,
    stacklevel=2,
)
sys.path.insert(0, str(pathlib.Path(__file__).absolute().parent / '_compat'))
