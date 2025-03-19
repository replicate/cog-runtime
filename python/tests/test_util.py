from typing import List, Optional

from coglet import adt, api, util


def test_get_field_type():
    assert util.get_field_type(int) == adt.FieldType(
        primitive=adt.PrimitiveType.INTEGER, repetition=adt.Repetition.REQUIRED
    )
    assert util.get_field_type(str) == adt.FieldType(
        primitive=adt.PrimitiveType.STRING, repetition=adt.Repetition.REQUIRED
    )
    assert util.get_field_type(Optional[int]) == adt.FieldType(
        primitive=adt.PrimitiveType.INTEGER, repetition=adt.Repetition.OPTIONAL
    )
    assert util.get_field_type(Optional[int]) == adt.FieldType(
        primitive=adt.PrimitiveType.INTEGER, repetition=adt.Repetition.OPTIONAL
    )
    assert util.get_field_type(list[int]) == adt.FieldType(
        primitive=adt.PrimitiveType.INTEGER, repetition=adt.Repetition.REPEATED
    )
    assert util.get_field_type(List[int]) == adt.FieldType(
        primitive=adt.PrimitiveType.INTEGER, repetition=adt.Repetition.REPEATED
    )


def test_check_value():
    assert util.check_value(adt.PrimitiveType.BOOL, True)
    assert not util.check_value(adt.PrimitiveType.BOOL, 1)
    assert util.check_value(adt.PrimitiveType.FLOAT, 1)
    assert util.check_value(adt.PrimitiveType.FLOAT, 3.14)
    assert not util.check_value(adt.PrimitiveType.FLOAT, 'foo')
    assert util.check_value(adt.PrimitiveType.INTEGER, 1)
    assert not util.check_value(adt.PrimitiveType.INTEGER, 3.14)
    assert util.check_value(adt.PrimitiveType.STRING, 'foo')
    assert not util.check_value(adt.PrimitiveType.STRING, 1)
    assert util.check_value(adt.PrimitiveType.PATH, api.Path('foo'))
    assert util.check_value(adt.PrimitiveType.PATH, 'foo')
    assert not util.check_value(adt.PrimitiveType.PATH, 1)
    assert util.check_value(adt.PrimitiveType.SECRET, api.Secret('foo'))
    assert util.check_value(adt.PrimitiveType.SECRET, 'foo')
    assert not util.check_value(adt.PrimitiveType.SECRET, 1)


def test_json_value():
    assert util.json_value(adt.PrimitiveType.BOOL, True)
    assert not util.json_value(adt.PrimitiveType.BOOL, False)
    assert util.json_value(adt.PrimitiveType.FLOAT, 1) == 1.0
    assert util.json_value(adt.PrimitiveType.FLOAT, 1.2) == 1.2
    assert util.json_value(adt.PrimitiveType.INTEGER, 3) == 3
    assert util.json_value(adt.PrimitiveType.STRING, 'foo') == 'foo'
    assert util.json_value(adt.PrimitiveType.PATH, 'foo.txt') == 'foo.txt'
    assert util.json_value(adt.PrimitiveType.PATH, api.Path('bar.txt')) == 'bar.txt'
    assert util.json_value(adt.PrimitiveType.SECRET, 'foo') == 'foo'
    assert util.json_value(adt.PrimitiveType.SECRET, api.Secret('bar')) == '**********'


def test_normalize_value():
    assert util.normalize_value(adt.PrimitiveType.BOOL, True)
    assert not util.normalize_value(adt.PrimitiveType.BOOL, False)
    assert util.normalize_value(adt.PrimitiveType.FLOAT, 1) == 1.0
    assert util.normalize_value(adt.PrimitiveType.FLOAT, 1.2) == 1.2
    assert util.normalize_value(adt.PrimitiveType.INTEGER, 3) == 3
    assert util.normalize_value(adt.PrimitiveType.STRING, 'foo') == 'foo'
    assert util.normalize_value(adt.PrimitiveType.PATH, 'foo.txt') == api.Path(
        'foo.txt'
    )
    assert util.normalize_value(
        adt.PrimitiveType.PATH, api.Path('bar.txt')
    ) == api.Path('bar.txt')
    assert util.normalize_value(adt.PrimitiveType.SECRET, 'foo') == api.Secret('foo')
    assert util.normalize_value(
        adt.PrimitiveType.SECRET, api.Secret('bar')
    ) == api.Secret('bar')
