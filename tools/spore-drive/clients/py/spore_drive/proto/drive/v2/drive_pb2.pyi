from google.protobuf.internal import containers as _containers
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class Document(_message.Message):
    __slots__ = ("files",)
    class FilesEntry(_message.Message):
        __slots__ = ("key", "value")
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: bytes
        def __init__(self, key: _Optional[str] = ..., value: _Optional[bytes] = ...) -> None: ...
    FILES_FIELD_NUMBER: _ClassVar[int]
    files: _containers.ScalarMap[str, bytes]
    def __init__(self, files: _Optional[_Mapping[str, bytes]] = ...) -> None: ...

class ValidateRequest(_message.Message):
    __slots__ = ("document",)
    DOCUMENT_FIELD_NUMBER: _ClassVar[int]
    document: Document
    def __init__(self, document: _Optional[_Union[Document, _Mapping]] = ...) -> None: ...

class ValidateResponse(_message.Message):
    __slots__ = ("errors", "ignored", "document")
    ERRORS_FIELD_NUMBER: _ClassVar[int]
    IGNORED_FIELD_NUMBER: _ClassVar[int]
    DOCUMENT_FIELD_NUMBER: _ClassVar[int]
    errors: _containers.RepeatedScalarFieldContainer[str]
    ignored: _containers.RepeatedScalarFieldContainer[str]
    document: Document
    def __init__(self, errors: _Optional[_Iterable[str]] = ..., ignored: _Optional[_Iterable[str]] = ..., document: _Optional[_Union[Document, _Mapping]] = ...) -> None: ...

class PlanRequest(_message.Message):
    __slots__ = ("document", "latest", "version", "url", "path", "shapes", "concurrency", "root")
    DOCUMENT_FIELD_NUMBER: _ClassVar[int]
    LATEST_FIELD_NUMBER: _ClassVar[int]
    VERSION_FIELD_NUMBER: _ClassVar[int]
    URL_FIELD_NUMBER: _ClassVar[int]
    PATH_FIELD_NUMBER: _ClassVar[int]
    SHAPES_FIELD_NUMBER: _ClassVar[int]
    CONCURRENCY_FIELD_NUMBER: _ClassVar[int]
    ROOT_FIELD_NUMBER: _ClassVar[int]
    document: Document
    latest: bool
    version: str
    url: str
    path: str
    shapes: _containers.RepeatedScalarFieldContainer[str]
    concurrency: int
    root: str
    def __init__(self, document: _Optional[_Union[Document, _Mapping]] = ..., latest: bool = ..., version: _Optional[str] = ..., url: _Optional[str] = ..., path: _Optional[str] = ..., shapes: _Optional[_Iterable[str]] = ..., concurrency: _Optional[int] = ..., root: _Optional[str] = ...) -> None: ...

class Course(_message.Message):
    __slots__ = ("id", "hops", "hosts")
    ID_FIELD_NUMBER: _ClassVar[int]
    HOPS_FIELD_NUMBER: _ClassVar[int]
    HOSTS_FIELD_NUMBER: _ClassVar[int]
    id: str
    hops: _containers.RepeatedCompositeFieldContainer[Hop]
    hosts: _containers.RepeatedCompositeFieldContainer[Host]
    def __init__(self, id: _Optional[str] = ..., hops: _Optional[_Iterable[_Union[Hop, _Mapping]]] = ..., hosts: _Optional[_Iterable[_Union[Host, _Mapping]]] = ...) -> None: ...

class Hop(_message.Message):
    __slots__ = ("name", "shapes", "hosts")
    NAME_FIELD_NUMBER: _ClassVar[int]
    SHAPES_FIELD_NUMBER: _ClassVar[int]
    HOSTS_FIELD_NUMBER: _ClassVar[int]
    name: str
    shapes: _containers.RepeatedScalarFieldContainer[str]
    hosts: _containers.RepeatedScalarFieldContainer[str]
    def __init__(self, name: _Optional[str] = ..., shapes: _Optional[_Iterable[str]] = ..., hosts: _Optional[_Iterable[str]] = ...) -> None: ...

class Host(_message.Message):
    __slots__ = ("name", "nodes", "ignored")
    NAME_FIELD_NUMBER: _ClassVar[int]
    NODES_FIELD_NUMBER: _ClassVar[int]
    IGNORED_FIELD_NUMBER: _ClassVar[int]
    name: str
    nodes: _containers.RepeatedCompositeFieldContainer[Node]
    ignored: _containers.RepeatedScalarFieldContainer[str]
    def __init__(self, name: _Optional[str] = ..., nodes: _Optional[_Iterable[_Union[Node, _Mapping]]] = ..., ignored: _Optional[_Iterable[str]] = ...) -> None: ...

class Node(_message.Message):
    __slots__ = ("name", "port", "services")
    NAME_FIELD_NUMBER: _ClassVar[int]
    PORT_FIELD_NUMBER: _ClassVar[int]
    SERVICES_FIELD_NUMBER: _ClassVar[int]
    name: str
    port: int
    services: _containers.RepeatedScalarFieldContainer[str]
    def __init__(self, name: _Optional[str] = ..., port: _Optional[int] = ..., services: _Optional[_Iterable[str]] = ...) -> None: ...

class ApplyRequest(_message.Message):
    __slots__ = ("course",)
    COURSE_FIELD_NUMBER: _ClassVar[int]
    course: str
    def __init__(self, course: _Optional[str] = ...) -> None: ...

class Progress(_message.Message):
    __slots__ = ("hop", "host", "step", "progress", "error")
    HOP_FIELD_NUMBER: _ClassVar[int]
    HOST_FIELD_NUMBER: _ClassVar[int]
    STEP_FIELD_NUMBER: _ClassVar[int]
    PROGRESS_FIELD_NUMBER: _ClassVar[int]
    ERROR_FIELD_NUMBER: _ClassVar[int]
    hop: str
    host: str
    step: str
    progress: int
    error: str
    def __init__(self, hop: _Optional[str] = ..., host: _Optional[str] = ..., step: _Optional[str] = ..., progress: _Optional[int] = ..., error: _Optional[str] = ...) -> None: ...

class AbortRequest(_message.Message):
    __slots__ = ("course",)
    COURSE_FIELD_NUMBER: _ClassVar[int]
    course: str
    def __init__(self, course: _Optional[str] = ...) -> None: ...

class AbortResponse(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...
