from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class Command(_message.Message):
    __slots__ = ("index", "command")
    INDEX_FIELD_NUMBER: _ClassVar[int]
    COMMAND_FIELD_NUMBER: _ClassVar[int]
    index: int
    command: str
    def __init__(self, index: _Optional[int] = ..., command: _Optional[str] = ...) -> None: ...

class Host(_message.Message):
    __slots__ = ("name",)
    NAME_FIELD_NUMBER: _ClassVar[int]
    name: str
    def __init__(self, name: _Optional[str] = ...) -> None: ...

class HostConfig(_message.Message):
    __slots__ = ("host", "port", "workdir", "passphrase", "private_key", "auth_username", "auth_password", "auth_privkey")
    HOST_FIELD_NUMBER: _ClassVar[int]
    PORT_FIELD_NUMBER: _ClassVar[int]
    WORKDIR_FIELD_NUMBER: _ClassVar[int]
    PASSPHRASE_FIELD_NUMBER: _ClassVar[int]
    PRIVATE_KEY_FIELD_NUMBER: _ClassVar[int]
    AUTH_USERNAME_FIELD_NUMBER: _ClassVar[int]
    AUTH_PASSWORD_FIELD_NUMBER: _ClassVar[int]
    AUTH_PRIVKEY_FIELD_NUMBER: _ClassVar[int]
    host: Host
    port: int
    workdir: str
    passphrase: str
    private_key: bytes
    auth_username: str
    auth_password: str
    auth_privkey: bytes
    def __init__(self, host: _Optional[_Union[Host, _Mapping]] = ..., port: _Optional[int] = ..., workdir: _Optional[str] = ..., passphrase: _Optional[str] = ..., private_key: _Optional[bytes] = ..., auth_username: _Optional[str] = ..., auth_password: _Optional[str] = ..., auth_privkey: _Optional[bytes] = ...) -> None: ...

class Query(_message.Message):
    __slots__ = ("name", "port")
    NAME_FIELD_NUMBER: _ClassVar[int]
    PORT_FIELD_NUMBER: _ClassVar[int]
    name: str
    port: int
    def __init__(self, name: _Optional[str] = ..., port: _Optional[int] = ...) -> None: ...

class BundleChunk(_message.Message):
    __slots__ = ("data",)
    DATA_FIELD_NUMBER: _ClassVar[int]
    data: bytes
    def __init__(self, data: _Optional[bytes] = ...) -> None: ...

class Empty(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...
