from config.v1 import config_pb2 as _config_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class DriveRequest(_message.Message):
    __slots__ = ("config", "latest", "version", "url", "path")
    CONFIG_FIELD_NUMBER: _ClassVar[int]
    LATEST_FIELD_NUMBER: _ClassVar[int]
    VERSION_FIELD_NUMBER: _ClassVar[int]
    URL_FIELD_NUMBER: _ClassVar[int]
    PATH_FIELD_NUMBER: _ClassVar[int]
    config: _config_pb2.Config
    latest: bool
    version: str
    url: str
    path: str
    def __init__(self, config: _Optional[_Union[_config_pb2.Config, _Mapping]] = ..., latest: bool = ..., version: _Optional[str] = ..., url: _Optional[str] = ..., path: _Optional[str] = ...) -> None: ...

class PlotRequest(_message.Message):
    __slots__ = ("drive", "shapes", "concurrency")
    DRIVE_FIELD_NUMBER: _ClassVar[int]
    SHAPES_FIELD_NUMBER: _ClassVar[int]
    CONCURRENCY_FIELD_NUMBER: _ClassVar[int]
    drive: Drive
    shapes: _containers.RepeatedScalarFieldContainer[str]
    concurrency: int
    def __init__(self, drive: _Optional[_Union[Drive, _Mapping]] = ..., shapes: _Optional[_Iterable[str]] = ..., concurrency: _Optional[int] = ...) -> None: ...

class Drive(_message.Message):
    __slots__ = ("id",)
    ID_FIELD_NUMBER: _ClassVar[int]
    id: str
    def __init__(self, id: _Optional[str] = ...) -> None: ...

class Course(_message.Message):
    __slots__ = ("id",)
    ID_FIELD_NUMBER: _ClassVar[int]
    id: str
    def __init__(self, id: _Optional[str] = ...) -> None: ...

class DisplacementProgress(_message.Message):
    __slots__ = ("path", "name", "progress", "error")
    PATH_FIELD_NUMBER: _ClassVar[int]
    NAME_FIELD_NUMBER: _ClassVar[int]
    PROGRESS_FIELD_NUMBER: _ClassVar[int]
    ERROR_FIELD_NUMBER: _ClassVar[int]
    path: str
    name: str
    progress: int
    error: str
    def __init__(self, path: _Optional[str] = ..., name: _Optional[str] = ..., progress: _Optional[int] = ..., error: _Optional[str] = ...) -> None: ...

class Empty(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...
