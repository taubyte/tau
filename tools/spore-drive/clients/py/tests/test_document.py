import os
import stat

import pytest

import spore_drive
from spore_drive import from_files, read_dir, write_dir


def test_from_files_encodes_str_and_keeps_bytes():
    doc = from_files({"cloud.yaml": "a: 1\n", "keys/swarm.key": b"\x00\x01"})
    assert dict(doc.files) == {"cloud.yaml": b"a: 1\n", "keys/swarm.key": b"\x00\x01"}


@pytest.mark.parametrize("bad", ["", "/etc/passwd", "../x", "keys/../../x"])
def test_from_files_refuses_paths_leaving_the_root(bad):
    with pytest.raises(ValueError):
        from_files({bad: "x"})


def test_round_trip(tmp_path):
    files = {"cloud.yaml": b"domain: {}\n", "keys/swarm.key": b"secret", "a/b/c.txt": b"deep"}
    write_dir(from_files(files), tmp_path / "out")
    assert stat.S_IMODE(os.stat(tmp_path / "out" / "keys" / "swarm.key").st_mode) == 0o600
    assert dict(read_dir(tmp_path / "out").files) == files


def test_write_dir_leaves_unnamed_files(tmp_path):
    (tmp_path / "other").write_text("keep")
    write_dir(from_files({"cloud.yaml": "x"}), tmp_path)
    assert (tmp_path / "other").read_text() == "keep"
    assert set(read_dir(tmp_path).files) == {"other", "cloud.yaml"}


def test_exports():
    for name in spore_drive.__all__:
        assert hasattr(spore_drive, name)
