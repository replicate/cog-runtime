import json
import os.path
from typing import Any, Dict

from coglet import adt, util


def _to_json_type(cog_t: adt.PrimitiveType, prop: Dict[str, Any]) -> None:
    prop['type'] = adt.COG_TO_JSON[cog_t]
    if cog_t is adt.PrimitiveType.PATH:
        prop['format'] = 'uri'
    elif cog_t is adt.PrimitiveType.SECRET:
        prop['format'] = 'password'
        prop['writeOnly'] = True
        prop['x-cog-secret'] = True


def to_json_input(predictor: adt.Predictor) -> Dict[str, Any]:
    in_schema: Dict[str, Any] = {
        'properties': {},
        'type': 'object',
        'title': 'Input',
    }
    required = []
    for name, adt_in in predictor.inputs.items():
        prop: Dict[str, Any] = {
            'x-order': adt_in.order,
        }
        if adt_in.choices is not None:
            prop['allOf'] = [{'$ref': f'#/components/schemas/{name}'}]
        else:
            prop['title'] = name.replace('_', ' ').title()
            json_t = adt.COG_TO_JSON[adt_in.type.primitive]
            if adt_in.type.repetition is adt.Repetition.REPEATED:
                prop['type'] = 'array'
                prop['items'] = {'type': json_t}
            else:
                prop['type'] = json_t
        if adt_in.type.primitive is adt.PrimitiveType.PATH:
            p = (
                prop['items']
                if adt_in.type.repetition is adt.Repetition.REPEATED
                else prop
            )
            p['format'] = 'uri'
        elif adt_in.type.primitive is adt.PrimitiveType.SECRET:
            p = (
                prop['items']
                if adt_in.type.repetition is adt.Repetition.REPEATED
                else prop
            )
            p['format'] = 'password'
            p['writeOnly'] = True
            p['x-cog-secret'] = True

        # With <name>: <type> = Input(default=None)
        # Legacy Cog does not include <name> in "required" fields or set "default" value
        # This allows None to be passed to `str` or `List[str]` which is incorrect

        # <name>: <type>
        if adt_in.type.repetition is adt.Repetition.REQUIRED:
            if adt_in.default is None:
                # default=None or unspecified: actual "required" field in schema
                required.append(name)
            else:
                prop['default'] = util.json_value(adt_in.type.primitive, adt_in.default)
        # <name>: Optional[<type>]
        elif adt_in.type.repetition is adt.Repetition.OPTIONAL:
            if adt_in.default is None:
                # default=None or unspecified: not "required" field in schema and defaults to None
                pass
            else:
                prop['default'] = util.json_value(adt_in.type.primitive, adt_in.default)
        # <name>: Optional[<type>]
        elif adt_in.type.repetition is adt.Repetition.REPEATED:
            if adt_in.default is None:
                # default=None or unspecified: actual "required" field in schema
                required.append(name)
            else:
                # default=[] is a valid default
                prop['default'] = [
                    util.json_value(adt_in.type.primitive, x) for x in adt_in.default
                ]

        if adt_in.description is not None:
            prop['description'] = adt_in.description
        if adt_in.ge is not None:
            prop['minimum'] = adt_in.ge
        if adt_in.le is not None:
            prop['maximum'] = adt_in.le
        if adt_in.min_length is not None:
            prop['minLength'] = adt_in.min_length
        if adt_in.max_length is not None:
            prop['maxLength'] = adt_in.max_length
        if adt_in.regex is not None:
            prop['pattern'] = adt_in.regex
        in_schema['properties'][name] = prop
    if len(required) > 0:
        in_schema['required'] = required
    return in_schema


def to_json_enums(predictor: adt.Predictor) -> Dict[str, Any]:
    enums = {}
    for name, adt_in in predictor.inputs.items():
        if adt_in.choices is None:
            continue
        enums[name] = {
            'title': name,
            'type': adt.COG_TO_JSON[adt_in.type.primitive],
            'description': 'An enumeration.',
            'enum': adt_in.choices,
        }
    return enums


def to_json_output(predictor: adt.Predictor) -> Dict[str, Any]:
    out_schema: Dict[str, Any] = {
        'title': 'Output',
    }
    output = predictor.output
    if output.kind is adt.Kind.SINGLE or output.kind in adt.ARRAY_KINDS:
        assert output.type is not None
        if output.kind is adt.Kind.SINGLE:
            _to_json_type(output.type, out_schema)
        else:
            out_schema['type'] = 'array'
            out_schema['items'] = {}
            _to_json_type(output.type, out_schema['items'])

        if output.kind is adt.Kind.ITERATOR:
            out_schema['x-cog-array-type'] = 'iterator'
        elif output.kind is adt.Kind.CONCAT_ITERATOR:
            out_schema['x-cog-array-display'] = 'concatenate'
            out_schema['x-cog-array-type'] = 'iterator'
    elif output.kind is adt.Kind.OBJECT:
        assert output.fields is not None
        out_schema['type'] = 'object'
        props = {}
        required = []
        for name, cog_t in output.fields.items():
            props[name] = {
                'title': name.replace('_', ' ').title(),
            }
            _to_json_type(cog_t.primitive, props[name])
            required.append(name)
        out_schema['properties'] = props
        out_schema['required'] = required
    return out_schema


def to_json_schema(predictor: adt.Predictor) -> Dict[str, Any]:
    path = os.path.join(os.path.dirname(__file__), 'openapi.json')
    with open(path, 'r') as f:
        schema = json.load(f)
    schema['components']['schemas']['Input'] = to_json_input(predictor)
    schema['components']['schemas']['Output'] = to_json_output(predictor)
    schema['components']['schemas'].update(to_json_enums(predictor))
    return schema
