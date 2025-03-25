import json
import os.path
from typing import Any, Dict

from coglet import adt


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
            prop.update(adt_in.type.json_type())

        # With <name>: <type> = Input(default=None)
        # Legacy Cog does not include <name> in "required" fields or set "default" value
        # This allows None to be passed to `str` or `List[str]` which is incorrect

        # <name>: <type>
        if adt_in.type.repetition is adt.Repetition.REQUIRED:
            if adt_in.default is None:
                # default=None or unspecified: actual "required" field in schema
                required.append(name)
            else:
                prop['default'] = adt_in.type.primitive.json_value(adt_in.default)
        # <name>: Optional[<type>]
        elif adt_in.type.repetition is adt.Repetition.OPTIONAL:
            if adt_in.default is None:
                # default=None or unspecified: not "required" field in schema and defaults to None
                pass
            else:
                prop['default'] = adt_in.type.primitive.json_value(adt_in.default)
        # <name>: Optional[<type>]
        elif adt_in.type.repetition is adt.Repetition.REPEATED:
            if adt_in.default is None:
                # default=None or unspecified: actual "required" field in schema
                required.append(name)
            else:
                # default=[] is a valid default
                prop['default'] = [
                    adt_in.type.primitive.json_value(x) for x in adt_in.default
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
        t = {
            'title': name,
            'description': 'An enumeration.',
            'enum': adt_in.choices,
        }
        t.update(adt_in.type.primitive.json_type())
        enums[name] = t
    return enums


def to_json_output(predictor: adt.Predictor) -> Dict[str, Any]:
    return predictor.output.json_type()


def to_json_schema(predictor: adt.Predictor) -> Dict[str, Any]:
    path = os.path.join(os.path.dirname(__file__), 'openapi.json')
    with open(path, 'r') as f:
        schema = json.load(f)
    schema['components']['schemas']['Input'] = to_json_input(predictor)
    schema['components']['schemas']['Output'] = to_json_output(predictor)
    schema['components']['schemas'].update(to_json_enums(predictor))
    return schema
