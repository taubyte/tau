"""
Shared op-building helpers.

The operation classes build a nested dict op tree (BaseOperation._build_op); the
client turns that into the concrete service Op message and dispatches it. Keeping
dict_to_protobuf / apply_op here (not on the ConfigService client) lets an ee
client reuse the exact same machinery to wrap a different service's op message.
"""


def _to_message(nested_type, value):
    """Build nested_type from value: a dict of fields, or a plain list for a
    single-repeated-field message like StringSlice (whose field is named
    "value", which would collide with the dict protocol's reserved key)."""
    if isinstance(value, list):
        msg = nested_type()
        for f in msg.DESCRIPTOR.fields:
            container = getattr(msg, f.name)
            if hasattr(container, "extend"):  # the repeated field's container
                container.extend(value)
                break
        return msg
    return dict_to_protobuf(value, nested_type)


def dict_to_protobuf(op_dict, message_type):
    """Convert a nested operation dictionary to a protobuf message."""
    msg = message_type()
    if not isinstance(op_dict, dict):
        return op_dict  # base case for leaf values

    for k, v in op_dict.items():
        if k == "case":
            field_name = op_dict["case"]
            if "value" in op_dict:
                value = op_dict["value"]
                field = msg.DESCRIPTOR.fields_by_name.get(field_name)
                if field is not None and field.message_type:
                    nested_type = field.message_type._concrete_class
                    getattr(msg, field_name).CopyFrom(_to_message(nested_type, value))
                else:
                    setattr(msg, field_name, value)
            else:
                setattr(msg, field_name, True)
        elif k == "value":
            continue
        elif k == "op":
            nested = dict_to_protobuf(v, message_type)
            for field in nested.DESCRIPTOR.fields:
                if field.message_type is not None:
                    if nested.HasField(field.name):
                        getattr(msg, field.name).CopyFrom(getattr(nested, field.name))
                else:
                    value = getattr(nested, field.name)
                    if value != field.default_value:
                        setattr(msg, field.name, value)
        elif k == "shape" or k == "name" or k == "domain":
            setattr(msg, k, v)
        else:
            field = msg.DESCRIPTOR.fields_by_name.get(k)
            if field is not None and field.message_type:
                nested_type = field.message_type._concrete_class
                getattr(msg, k).CopyFrom(_to_message(nested_type, v))
            else:
                setattr(msg, k, v)
    return msg


def apply_op(final_op, dict_op):
    """Set the oneof field named by dict_op['case'] on final_op from its value.

    Generic over the service op message, so both ConfigService (cloud/hosts/...)
    and the ee EnterpriseService use it.
    """
    case_name = dict_op["case"]
    field = getattr(final_op, case_name)
    sub = dict_to_protobuf(dict_op["value"], type(field))
    field.CopyFrom(sub)
