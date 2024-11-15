import json
import os.path
from typing import Any, Dict, Optional, Union

from cog.internal import adt, util


def _from_json_type(prop: Dict[str, Any]) -> adt.Type:
    cog_t = adt.JSON_TO_COG[prop['type']]
    if cog_t is adt.Type.STRING:
        fmt = prop.get('format')
        if fmt == 'uri':
            return adt.Type.PATH
        elif fmt == 'password':
            return adt.Type.SECRET
    return cog_t


def _to_json_type(cog_t: adt.Type, prop: Dict[str, Any]) -> None:
    prop['type'] = adt.COG_TO_JSON[cog_t]
    if cog_t is adt.Type.PATH:
        prop['format'] = 'uri'
    elif cog_t is adt.Type.SECRET:
        prop['format'] = 'password'
        prop['writeOnly'] = True
        prop['x-cog-secret'] = True


def from_json_input(schema: Dict[str, Any]) -> Dict[str, adt.Input]:
    inputs = {}

    def cast_min_max(
        tpe: adt.Type, v: Optional[Union[float, int]]
    ) -> Optional[Union[float, int]]:
        if v is None:
            return v
        return int(v) if tpe is adt.Type.INTEGER else float(v)

    schemas = schema['components']['schemas']
    for name, prop in schemas['Input']['properties'].items():
        if 'type' not in prop and 'allOf' in prop:
            p = schemas[name]
            cog_t = adt.JSON_TO_COG[p['type']]
            is_list = False
            choices = p['enum']
        else:
            if prop['type'] == 'array':
                is_list = True
                cog_t = _from_json_type(prop['items'])
            else:
                is_list = False
                cog_t = _from_json_type(prop)
            choices = None
        default = prop.get('default')
        if default is not None:
            if is_list:
                default = [util.normalize_value(cog_t, v) for v in default]
            else:
                default = util.normalize_value(cog_t, default)
        inputs[name] = adt.Input(
            name=name,
            order=prop['x-order'],
            type=cog_t,
            is_list=is_list,
            default=default,
            description=prop.get('description'),
            ge=cast_min_max(cog_t, prop.get('minimum')),
            le=cast_min_max(cog_t, prop.get('maximum')),
            min_length=prop.get('minLength'),
            max_length=prop.get('maxLength'),
            regex=prop.get('pattern'),
            choices=choices,
        )
    return inputs


def from_json_output(schema: Dict[str, Any]) -> adt.Output:
    out_schema = schema['components']['schemas']['Output']
    json_t = out_schema['type']

    if json_t == 'object':
        fields = {}
        for name, prop in out_schema['properties'].items():
            fields[name] = _from_json_type(prop)
        return adt.Output(kind=adt.Kind.OBJECT, type=None, fields=fields)
    else:
        if json_t == 'array':
            if out_schema.get('x-cog-array-display') == 'concatenate':
                kind = adt.Kind.CONCAT_ITERATOR
            elif out_schema.get('x-cog-array-type') == 'iterator':
                kind = adt.Kind.ITERATOR
            else:
                kind = adt.Kind.LIST
            cog_t = _from_json_type(out_schema['items'])
        else:
            kind = adt.Kind.SINGLE
            cog_t = _from_json_type(out_schema)
        return adt.Output(
            kind=kind,
            type=cog_t,
            fields=None,
        )


def from_json_schema(
    module_name: str, class_name: str, schema: Dict[str, Any]
) -> adt.Predictor:
    inputs = from_json_input(schema)
    output = from_json_output(schema)
    return adt.Predictor(
        module_name=module_name,
        class_name=class_name,
        inputs=inputs,
        output=output,
    )


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
            json_t = adt.COG_TO_JSON[adt_in.type]
            if adt_in.is_list:
                prop['type'] = 'array'
                prop['items'] = {'type': json_t}
            else:
                prop['type'] = json_t
        if adt_in.type is adt.Type.PATH:
            p = prop['items'] if adt_in.is_list else prop
            p['format'] = 'uri'
        elif adt_in.type is adt.Type.SECRET:
            p = prop['items'] if adt_in.is_list else prop
            p['format'] = 'password'
            p['writeOnly'] = True
            p['x-cog-secret'] = True

        if adt_in.default is None:
            required.append(name)
        else:
            if adt_in.is_list:
                prop['default'] = [
                    util.json_value(adt_in.type, x) for x in adt_in.default
                ]
            else:
                prop['default'] = util.json_value(adt_in.type, adt_in.default)

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
            'type': adt.COG_TO_JSON[adt_in.type],
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
            _to_json_type(cog_t, props[name])
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
