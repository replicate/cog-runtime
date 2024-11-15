from typing import List

from cog import Path, Secret
from cog.internal import adt, util


def test_check_cog_type():
    assert util.check_cog_type(int) == (adt.Type.INTEGER, False)
    assert util.check_cog_type(str) == (adt.Type.STRING, False)
    assert util.check_cog_type(list[int]) == (adt.Type.INTEGER, True)
    assert util.check_cog_type(List[int]) == (adt.Type.INTEGER, True)


def test_check_value():
    assert util.check_value(adt.Type.BOOL, True)
    assert not util.check_value(adt.Type.BOOL, 1)
    assert util.check_value(adt.Type.FLOAT, 1)
    assert util.check_value(adt.Type.FLOAT, 3.14)
    assert not util.check_value(adt.Type.FLOAT, 'foo')
    assert util.check_value(adt.Type.INTEGER, 1)
    assert not util.check_value(adt.Type.INTEGER, 3.14)
    assert util.check_value(adt.Type.STRING, 'foo')
    assert not util.check_value(adt.Type.STRING, 1)
    assert util.check_value(adt.Type.PATH, Path('foo'))
    assert util.check_value(adt.Type.PATH, 'foo')
    assert not util.check_value(adt.Type.PATH, 1)
    assert util.check_value(adt.Type.SECRET, Secret('foo'))
    assert util.check_value(adt.Type.SECRET, 'foo')
    assert not util.check_value(adt.Type.SECRET, 1)


def test_json_value():
    assert util.json_value(adt.Type.BOOL, True)
    assert not util.json_value(adt.Type.BOOL, False)
    assert util.json_value(adt.Type.FLOAT, 1) == 1.0
    assert util.json_value(adt.Type.FLOAT, 1.2) == 1.2
    assert util.json_value(adt.Type.INTEGER, 3) == 3
    assert util.json_value(adt.Type.STRING, 'foo') == 'foo'
    assert util.json_value(adt.Type.PATH, 'foo.txt') == 'foo.txt'
    assert util.json_value(adt.Type.PATH, Path('bar.txt')) == 'bar.txt'
    assert util.json_value(adt.Type.SECRET, 'foo') == 'foo'
    assert util.json_value(adt.Type.SECRET, Secret('bar')) == '**********'


def test_normalize_value():
    assert util.normalize_value(adt.Type.BOOL, True)
    assert not util.normalize_value(adt.Type.BOOL, False)
    assert util.normalize_value(adt.Type.FLOAT, 1) == 1.0
    assert util.normalize_value(adt.Type.FLOAT, 1.2) == 1.2
    assert util.normalize_value(adt.Type.INTEGER, 3) == 3
    assert util.normalize_value(adt.Type.STRING, 'foo') == 'foo'
    assert util.normalize_value(adt.Type.PATH, 'foo.txt') == Path('foo.txt')
    assert util.normalize_value(adt.Type.PATH, Path('bar.txt')) == Path('bar.txt')
    assert util.normalize_value(adt.Type.SECRET, 'foo') == Secret('foo')
    assert util.normalize_value(adt.Type.SECRET, Secret('bar')) == Secret('bar')
