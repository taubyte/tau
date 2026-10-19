"""The cloud config directory as a drive.v2 Document.

A Document is every file of the config by its path relative to the config
root. This module only moves files between disk and the message; it reads
none of them, so a field the service learns needs no change here.
"""

import os
from pathlib import Path, PurePosixPath
from typing import Dict, Mapping, Union

from .proto.drive.v2 import drive_pb2

Content = Union[str, bytes]


def _check(rel: str) -> str:
    p = PurePosixPath(rel)
    if not rel or p.is_absolute() or ".." in p.parts:
        raise ValueError(f"document path {rel!r} must be relative and stay inside the root")
    return str(p)


def from_files(files: Mapping[str, Content]) -> drive_pb2.Document:
    """Build a Document from {relative path: content}; str is utf-8 encoded."""
    doc = drive_pb2.Document()
    for rel, content in files.items():
        if isinstance(content, str):
            content = content.encode()
        doc.files[_check(rel)] = content
    return doc


def read_dir(root: Union[str, os.PathLike]) -> drive_pb2.Document:
    """Read every file under root into a Document."""
    root = Path(root)
    files: Dict[str, bytes] = {}
    for path in sorted(root.rglob("*")):
        if path.is_file():
            files[path.relative_to(root).as_posix()] = path.read_bytes()
    return from_files(files)


def write_dir(doc: drive_pb2.Document, root: Union[str, os.PathLike]) -> None:
    """Write a Document's files under root, creating directories as needed.

    Files under root that the document does not name are left alone. Key
    files are written owner-only, since the document carries secrets.
    """
    root = Path(root)
    for rel, content in doc.files.items():
        path = root / _check(rel)
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(content)
        os.chmod(path, 0o600)
