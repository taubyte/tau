from google.protobuf.internal import containers as _containers
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class BundleType(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    BUNDLE_ZIP: _ClassVar[BundleType]
    BUNDLE_TAR: _ClassVar[BundleType]
BUNDLE_ZIP: BundleType
BUNDLE_TAR: BundleType

class Source(_message.Message):
    __slots__ = ("root", "path")
    ROOT_FIELD_NUMBER: _ClassVar[int]
    PATH_FIELD_NUMBER: _ClassVar[int]
    root: str
    path: str
    def __init__(self, root: _Optional[str] = ..., path: _Optional[str] = ...) -> None: ...

class SourceUpload(_message.Message):
    __slots__ = ("chunk", "path")
    CHUNK_FIELD_NUMBER: _ClassVar[int]
    PATH_FIELD_NUMBER: _ClassVar[int]
    chunk: bytes
    path: str
    def __init__(self, chunk: _Optional[bytes] = ..., path: _Optional[str] = ...) -> None: ...

class Config(_message.Message):
    __slots__ = ("id",)
    ID_FIELD_NUMBER: _ClassVar[int]
    id: str
    def __init__(self, id: _Optional[str] = ...) -> None: ...

class Bundle(_message.Message):
    __slots__ = ("type", "chunk")
    TYPE_FIELD_NUMBER: _ClassVar[int]
    CHUNK_FIELD_NUMBER: _ClassVar[int]
    type: BundleType
    chunk: bytes
    def __init__(self, type: _Optional[_Union[BundleType, str]] = ..., chunk: _Optional[bytes] = ...) -> None: ...

class StringOp(_message.Message):
    __slots__ = ("set", "get")
    SET_FIELD_NUMBER: _ClassVar[int]
    GET_FIELD_NUMBER: _ClassVar[int]
    set: str
    get: bool
    def __init__(self, set: _Optional[str] = ..., get: bool = ...) -> None: ...

class BytesOp(_message.Message):
    __slots__ = ("set", "get")
    SET_FIELD_NUMBER: _ClassVar[int]
    GET_FIELD_NUMBER: _ClassVar[int]
    set: bytes
    get: bool
    def __init__(self, set: _Optional[bytes] = ..., get: bool = ...) -> None: ...

class StringSliceOp(_message.Message):
    __slots__ = ("set", "add", "delete", "clear", "list")
    SET_FIELD_NUMBER: _ClassVar[int]
    ADD_FIELD_NUMBER: _ClassVar[int]
    DELETE_FIELD_NUMBER: _ClassVar[int]
    CLEAR_FIELD_NUMBER: _ClassVar[int]
    LIST_FIELD_NUMBER: _ClassVar[int]
    set: StringSlice
    add: StringSlice
    delete: StringSlice
    clear: bool
    list: bool
    def __init__(self, set: _Optional[_Union[StringSlice, _Mapping]] = ..., add: _Optional[_Union[StringSlice, _Mapping]] = ..., delete: _Optional[_Union[StringSlice, _Mapping]] = ..., clear: bool = ..., list: bool = ...) -> None: ...

class ReturnValue(_message.Message):
    __slots__ = ("value", "data")
    VALUE_FIELD_NUMBER: _ClassVar[int]
    DATA_FIELD_NUMBER: _ClassVar[int]
    value: StringOp
    data: BytesOp
    def __init__(self, value: _Optional[_Union[StringOp, _Mapping]] = ..., data: _Optional[_Union[BytesOp, _Mapping]] = ...) -> None: ...

class StringSlice(_message.Message):
    __slots__ = ("value",)
    VALUE_FIELD_NUMBER: _ClassVar[int]
    value: _containers.RepeatedScalarFieldContainer[str]
    def __init__(self, value: _Optional[_Iterable[str]] = ...) -> None: ...

class Error(_message.Message):
    __slots__ = ("code", "message")
    CODE_FIELD_NUMBER: _ClassVar[int]
    MESSAGE_FIELD_NUMBER: _ClassVar[int]
    code: int
    message: str
    def __init__(self, code: _Optional[int] = ..., message: _Optional[str] = ...) -> None: ...

class BundleConfig(_message.Message):
    __slots__ = ("id", "type")
    ID_FIELD_NUMBER: _ClassVar[int]
    TYPE_FIELD_NUMBER: _ClassVar[int]
    id: Config
    type: BundleType
    def __init__(self, id: _Optional[_Union[Config, _Mapping]] = ..., type: _Optional[_Union[BundleType, str]] = ...) -> None: ...

class Empty(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class Return(_message.Message):
    __slots__ = ("empty", "string", "slice", "bytes", "uint64", "int64", "float", "error")
    EMPTY_FIELD_NUMBER: _ClassVar[int]
    STRING_FIELD_NUMBER: _ClassVar[int]
    SLICE_FIELD_NUMBER: _ClassVar[int]
    BYTES_FIELD_NUMBER: _ClassVar[int]
    UINT64_FIELD_NUMBER: _ClassVar[int]
    INT64_FIELD_NUMBER: _ClassVar[int]
    FLOAT_FIELD_NUMBER: _ClassVar[int]
    ERROR_FIELD_NUMBER: _ClassVar[int]
    empty: Empty
    string: str
    slice: StringSlice
    bytes: bytes
    uint64: int
    int64: int
    float: float
    error: Error
    def __init__(self, empty: _Optional[_Union[Empty, _Mapping]] = ..., string: _Optional[str] = ..., slice: _Optional[_Union[StringSlice, _Mapping]] = ..., bytes: _Optional[bytes] = ..., uint64: _Optional[int] = ..., int64: _Optional[int] = ..., float: _Optional[float] = ..., error: _Optional[_Union[Error, _Mapping]] = ...) -> None: ...

class Op(_message.Message):
    __slots__ = ("config", "cloud", "hosts", "auth", "shapes")
    CONFIG_FIELD_NUMBER: _ClassVar[int]
    CLOUD_FIELD_NUMBER: _ClassVar[int]
    HOSTS_FIELD_NUMBER: _ClassVar[int]
    AUTH_FIELD_NUMBER: _ClassVar[int]
    SHAPES_FIELD_NUMBER: _ClassVar[int]
    config: Config
    cloud: Cloud
    hosts: Hosts
    auth: Auth
    shapes: Shapes
    def __init__(self, config: _Optional[_Union[Config, _Mapping]] = ..., cloud: _Optional[_Union[Cloud, _Mapping]] = ..., hosts: _Optional[_Union[Hosts, _Mapping]] = ..., auth: _Optional[_Union[Auth, _Mapping]] = ..., shapes: _Optional[_Union[Shapes, _Mapping]] = ...) -> None: ...

class Cloud(_message.Message):
    __slots__ = ("domain", "p2p")
    DOMAIN_FIELD_NUMBER: _ClassVar[int]
    P2P_FIELD_NUMBER: _ClassVar[int]
    domain: Domain
    p2p: P2P
    def __init__(self, domain: _Optional[_Union[Domain, _Mapping]] = ..., p2p: _Optional[_Union[P2P, _Mapping]] = ...) -> None: ...

class Domain(_message.Message):
    __slots__ = ("root", "generated", "validation")
    ROOT_FIELD_NUMBER: _ClassVar[int]
    GENERATED_FIELD_NUMBER: _ClassVar[int]
    VALIDATION_FIELD_NUMBER: _ClassVar[int]
    root: StringOp
    generated: StringOp
    validation: Validation
    def __init__(self, root: _Optional[_Union[StringOp, _Mapping]] = ..., generated: _Optional[_Union[StringOp, _Mapping]] = ..., validation: _Optional[_Union[Validation, _Mapping]] = ...) -> None: ...

class Validation(_message.Message):
    __slots__ = ("keys", "generate")
    KEYS_FIELD_NUMBER: _ClassVar[int]
    GENERATE_FIELD_NUMBER: _ClassVar[int]
    keys: ValidationKeys
    generate: bool
    def __init__(self, keys: _Optional[_Union[ValidationKeys, _Mapping]] = ..., generate: bool = ...) -> None: ...

class ValidationKeys(_message.Message):
    __slots__ = ("path", "data")
    PATH_FIELD_NUMBER: _ClassVar[int]
    DATA_FIELD_NUMBER: _ClassVar[int]
    path: ValidationKeysPath
    data: ValidationKeysData
    def __init__(self, path: _Optional[_Union[ValidationKeysPath, _Mapping]] = ..., data: _Optional[_Union[ValidationKeysData, _Mapping]] = ...) -> None: ...

class ValidationKeysPath(_message.Message):
    __slots__ = ("private_key", "public_key")
    PRIVATE_KEY_FIELD_NUMBER: _ClassVar[int]
    PUBLIC_KEY_FIELD_NUMBER: _ClassVar[int]
    private_key: StringOp
    public_key: StringOp
    def __init__(self, private_key: _Optional[_Union[StringOp, _Mapping]] = ..., public_key: _Optional[_Union[StringOp, _Mapping]] = ...) -> None: ...

class ValidationKeysData(_message.Message):
    __slots__ = ("private_key", "public_key")
    PRIVATE_KEY_FIELD_NUMBER: _ClassVar[int]
    PUBLIC_KEY_FIELD_NUMBER: _ClassVar[int]
    private_key: BytesOp
    public_key: BytesOp
    def __init__(self, private_key: _Optional[_Union[BytesOp, _Mapping]] = ..., public_key: _Optional[_Union[BytesOp, _Mapping]] = ...) -> None: ...

class P2P(_message.Message):
    __slots__ = ("bootstrap", "swarm")
    BOOTSTRAP_FIELD_NUMBER: _ClassVar[int]
    SWARM_FIELD_NUMBER: _ClassVar[int]
    bootstrap: Bootstrap
    swarm: Swarm
    def __init__(self, bootstrap: _Optional[_Union[Bootstrap, _Mapping]] = ..., swarm: _Optional[_Union[Swarm, _Mapping]] = ...) -> None: ...

class Bootstrap(_message.Message):
    __slots__ = ("select", "list")
    SELECT_FIELD_NUMBER: _ClassVar[int]
    LIST_FIELD_NUMBER: _ClassVar[int]
    select: BootstrapShape
    list: bool
    def __init__(self, select: _Optional[_Union[BootstrapShape, _Mapping]] = ..., list: bool = ...) -> None: ...

class BootstrapShape(_message.Message):
    __slots__ = ("shape", "nodes", "delete")
    SHAPE_FIELD_NUMBER: _ClassVar[int]
    NODES_FIELD_NUMBER: _ClassVar[int]
    DELETE_FIELD_NUMBER: _ClassVar[int]
    shape: str
    nodes: StringSliceOp
    delete: bool
    def __init__(self, shape: _Optional[str] = ..., nodes: _Optional[_Union[StringSliceOp, _Mapping]] = ..., delete: bool = ...) -> None: ...

class Swarm(_message.Message):
    __slots__ = ("key", "generate")
    KEY_FIELD_NUMBER: _ClassVar[int]
    GENERATE_FIELD_NUMBER: _ClassVar[int]
    key: SwarmKey
    generate: bool
    def __init__(self, key: _Optional[_Union[SwarmKey, _Mapping]] = ..., generate: bool = ...) -> None: ...

class SwarmKey(_message.Message):
    __slots__ = ("path", "data")
    PATH_FIELD_NUMBER: _ClassVar[int]
    DATA_FIELD_NUMBER: _ClassVar[int]
    path: StringOp
    data: BytesOp
    def __init__(self, path: _Optional[_Union[StringOp, _Mapping]] = ..., data: _Optional[_Union[BytesOp, _Mapping]] = ...) -> None: ...

class Hosts(_message.Message):
    __slots__ = ("select", "list")
    SELECT_FIELD_NUMBER: _ClassVar[int]
    LIST_FIELD_NUMBER: _ClassVar[int]
    select: Host
    list: bool
    def __init__(self, select: _Optional[_Union[Host, _Mapping]] = ..., list: bool = ...) -> None: ...

class Host(_message.Message):
    __slots__ = ("name", "addresses", "ssh", "location", "shapes", "delete")
    NAME_FIELD_NUMBER: _ClassVar[int]
    ADDRESSES_FIELD_NUMBER: _ClassVar[int]
    SSH_FIELD_NUMBER: _ClassVar[int]
    LOCATION_FIELD_NUMBER: _ClassVar[int]
    SHAPES_FIELD_NUMBER: _ClassVar[int]
    DELETE_FIELD_NUMBER: _ClassVar[int]
    name: str
    addresses: StringSliceOp
    ssh: SSH
    location: StringOp
    shapes: HostShapes
    delete: bool
    def __init__(self, name: _Optional[str] = ..., addresses: _Optional[_Union[StringSliceOp, _Mapping]] = ..., ssh: _Optional[_Union[SSH, _Mapping]] = ..., location: _Optional[_Union[StringOp, _Mapping]] = ..., shapes: _Optional[_Union[HostShapes, _Mapping]] = ..., delete: bool = ...) -> None: ...

class SSH(_message.Message):
    __slots__ = ("address", "auth")
    ADDRESS_FIELD_NUMBER: _ClassVar[int]
    AUTH_FIELD_NUMBER: _ClassVar[int]
    address: StringOp
    auth: StringSliceOp
    def __init__(self, address: _Optional[_Union[StringOp, _Mapping]] = ..., auth: _Optional[_Union[StringSliceOp, _Mapping]] = ...) -> None: ...

class HostShapes(_message.Message):
    __slots__ = ("select", "list")
    SELECT_FIELD_NUMBER: _ClassVar[int]
    LIST_FIELD_NUMBER: _ClassVar[int]
    select: HostShape
    list: bool
    def __init__(self, select: _Optional[_Union[HostShape, _Mapping]] = ..., list: bool = ...) -> None: ...

class HostShape(_message.Message):
    __slots__ = ("name", "select", "delete")
    NAME_FIELD_NUMBER: _ClassVar[int]
    SELECT_FIELD_NUMBER: _ClassVar[int]
    DELETE_FIELD_NUMBER: _ClassVar[int]
    name: str
    select: HostInstance
    delete: bool
    def __init__(self, name: _Optional[str] = ..., select: _Optional[_Union[HostInstance, _Mapping]] = ..., delete: bool = ...) -> None: ...

class HostInstance(_message.Message):
    __slots__ = ("id", "key", "generate")
    ID_FIELD_NUMBER: _ClassVar[int]
    KEY_FIELD_NUMBER: _ClassVar[int]
    GENERATE_FIELD_NUMBER: _ClassVar[int]
    id: bool
    key: StringOp
    generate: bool
    def __init__(self, id: bool = ..., key: _Optional[_Union[StringOp, _Mapping]] = ..., generate: bool = ...) -> None: ...

class Auth(_message.Message):
    __slots__ = ("select", "list")
    SELECT_FIELD_NUMBER: _ClassVar[int]
    LIST_FIELD_NUMBER: _ClassVar[int]
    select: Signer
    list: bool
    def __init__(self, select: _Optional[_Union[Signer, _Mapping]] = ..., list: bool = ...) -> None: ...

class Signer(_message.Message):
    __slots__ = ("name", "username", "password", "key", "delete")
    NAME_FIELD_NUMBER: _ClassVar[int]
    USERNAME_FIELD_NUMBER: _ClassVar[int]
    PASSWORD_FIELD_NUMBER: _ClassVar[int]
    KEY_FIELD_NUMBER: _ClassVar[int]
    DELETE_FIELD_NUMBER: _ClassVar[int]
    name: str
    username: StringOp
    password: StringOp
    key: SSHKey
    delete: bool
    def __init__(self, name: _Optional[str] = ..., username: _Optional[_Union[StringOp, _Mapping]] = ..., password: _Optional[_Union[StringOp, _Mapping]] = ..., key: _Optional[_Union[SSHKey, _Mapping]] = ..., delete: bool = ...) -> None: ...

class SSHKey(_message.Message):
    __slots__ = ("path", "data")
    PATH_FIELD_NUMBER: _ClassVar[int]
    DATA_FIELD_NUMBER: _ClassVar[int]
    path: StringOp
    data: BytesOp
    def __init__(self, path: _Optional[_Union[StringOp, _Mapping]] = ..., data: _Optional[_Union[BytesOp, _Mapping]] = ...) -> None: ...

class Shapes(_message.Message):
    __slots__ = ("select", "list")
    SELECT_FIELD_NUMBER: _ClassVar[int]
    LIST_FIELD_NUMBER: _ClassVar[int]
    select: Shape
    list: bool
    def __init__(self, select: _Optional[_Union[Shape, _Mapping]] = ..., list: bool = ...) -> None: ...

class Shape(_message.Message):
    __slots__ = ("name", "services", "ports", "plugins", "delete")
    NAME_FIELD_NUMBER: _ClassVar[int]
    SERVICES_FIELD_NUMBER: _ClassVar[int]
    PORTS_FIELD_NUMBER: _ClassVar[int]
    PLUGINS_FIELD_NUMBER: _ClassVar[int]
    DELETE_FIELD_NUMBER: _ClassVar[int]
    name: str
    services: StringSliceOp
    ports: Ports
    plugins: StringSliceOp
    delete: bool
    def __init__(self, name: _Optional[str] = ..., services: _Optional[_Union[StringSliceOp, _Mapping]] = ..., ports: _Optional[_Union[Ports, _Mapping]] = ..., plugins: _Optional[_Union[StringSliceOp, _Mapping]] = ..., delete: bool = ...) -> None: ...

class Ports(_message.Message):
    __slots__ = ("select", "list")
    SELECT_FIELD_NUMBER: _ClassVar[int]
    LIST_FIELD_NUMBER: _ClassVar[int]
    select: Port
    list: bool
    def __init__(self, select: _Optional[_Union[Port, _Mapping]] = ..., list: bool = ...) -> None: ...

class Port(_message.Message):
    __slots__ = ("name", "set", "get", "delete")
    NAME_FIELD_NUMBER: _ClassVar[int]
    SET_FIELD_NUMBER: _ClassVar[int]
    GET_FIELD_NUMBER: _ClassVar[int]
    DELETE_FIELD_NUMBER: _ClassVar[int]
    name: str
    set: int
    get: bool
    delete: bool
    def __init__(self, name: _Optional[str] = ..., set: _Optional[int] = ..., get: bool = ..., delete: bool = ...) -> None: ...
