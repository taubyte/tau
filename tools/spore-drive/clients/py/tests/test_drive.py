"""Drive against the Go mock: validate, plan, apply, abort."""

import uuid

import grpc
import pytest
import yaml

from spore_drive import Drive, from_files, read_dir, write_dir
from spore_drive.drive import _target
from tests.mock import MockSSH, config_files, fake_tau


def test_target_strips_scheme():
    assert _target("http://localhost:1/") == "localhost:1"
    assert _target("https://h:2") == "h:2"
    assert _target("h:3") == "h:3"


async def _cloud(mock_url, n=1):
    ssh = MockSSH(mock_url)
    ports = {}
    for _ in range(n):
        name = f"h-{uuid.uuid4().hex[:8]}"
        ports[name] = await ssh.new_host(name)
    return ssh, ports


async def test_plan_refuses_an_incomplete_document(mock_url, tmp_path):
    ssh, ports = await _cloud(mock_url)
    async with Drive(mock_url) as d:
        with pytest.raises(grpc.aio.AioRpcError) as e:
            await d.plan(from_files(config_files(ports)), path=fake_tau(str(tmp_path)))
        assert e.value.code() == grpc.StatusCode.FAILED_PRECONDITION
        assert "Validate generates them" in e.value.details()
    await ssh.close()


async def test_validate_lists_errors(mock_url):
    async with Drive(mock_url) as d:
        res = await d.validate(from_files({"cloud.yaml": "domain: [\n"}))
        assert res.errors


async def test_plan_takes_one_tau_source(mock_url):
    async with Drive(mock_url) as d:
        with pytest.raises(ValueError):
            await d.plan(from_files({}), latest=True, version="v1")


async def test_validate_plan_apply(mock_url, tmp_path):
    ssh, ports = await _cloud(mock_url)
    (name,) = ports
    cfg = tmp_path / "config"
    write_dir(from_files(config_files(ports)), cfg)

    async with Drive(mock_url) as d:
        res = await d.validate(read_dir(cfg))
        assert list(res.errors) == []
        # the secrets were generated and written in
        files = res.document.files
        assert "keys/swarm.key" in files
        assert yaml.safe_load(files["hosts.yaml"])[name]["shapes"]["all"]["key"]
        write_dir(res.document, cfg)

        doc = read_dir(cfg)
        again = await d.validate(doc)
        assert dict(again.document.files) == dict(doc.files)  # stable once complete

        course = await d.plan(doc, path=fake_tau(str(tmp_path)), shapes=["all"], concurrency=1)
        assert course.id
        assert [list(h.hosts) for h in course.hops] == [[name]]
        assert course.hosts[0].nodes[0].port == 4242

        progress = [p async for p in d.apply(course)]
        assert progress
        assert not [p for p in progress if p.error], [p.error for p in progress if p.error]
        assert max(p.progress for p in progress) == 100

        # the course ended with the stream
        with pytest.raises(grpc.aio.AioRpcError) as e:
            await d.abort(course)
        assert e.value.code() == grpc.StatusCode.NOT_FOUND

    cmds = await ssh.commands(name)
    assert "SFTP Filewrite /tmp/node.yaml" in cmds
    assert 'sudo "cp" "-f" "/tmp/node.yaml" "/tb/config/"' in cmds
    assert 'sudo "cp" "-f" "/tmp/tau" "/tb/bin/"' in cmds
    await ssh.close()


async def test_abort_an_unapplied_course(mock_url, tmp_path):
    ssh, ports = await _cloud(mock_url)
    async with Drive(mock_url) as d:
        res = await d.validate(from_files(config_files(ports)))
        course = await d.plan(res.document, path=fake_tau(str(tmp_path)))
        await d.abort(course.id)
        with pytest.raises(grpc.aio.AioRpcError) as e:
            async for _ in d.apply(course):
                pass
        assert e.value.code() == grpc.StatusCode.NOT_FOUND
    await ssh.close()
