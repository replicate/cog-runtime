import pathlib
import sys
import warnings

try:
    from ._version import __version__
except ImportError:
    __version__ = '0.0.0+unknown'

warnings.warn(
    (
        'coglet/_compat/ is being added to the front of sys.path '
        "for 'cog' import compatibility"
    ),
    category=ImportWarning,
    stacklevel=2,
)
sys.path.insert(0, str(pathlib.Path(__file__).absolute().parent / '_compat'))
