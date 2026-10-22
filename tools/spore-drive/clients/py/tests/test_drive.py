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
    write_dir(from_files(config_files(ports, added="example:\n  greeting: hello\n  replicas: 2\n",
                                      tenancy="provider: github\nowner: Acme\n")), cfg)

    async with Drive(mock_url) as d:
        res = await d.validate(read_dir(cfg))
        assert list(res.errors) == []
        # the secrets were generated and written in
        files = res.document.files
        assert "keys/ca.crt" in files and "keys/ca.key" in files
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
    assert "SFTP Filewrite /tmp/all.yaml" in cmds
    assert 'sudo "cp" "-f" "/tmp/all.yaml" "/tb/config/"' in cmds
    assert 'sudo "/tb/bin/tau" "init" "--config" "/tb/config/all.yaml" "--root" "/tb/storage/all" "-"' in cmds
    assert 'sudo "systemctl" "start" "tau@all"' in cmds
    assert 'sudo "cp" "-f" "/tmp/tau" "/tb/bin/"' in cmds

    # what was installed is the node the config describes, read off the host
    disk = await ssh.filesystem(name)
    raw = disk["/tb/config/all.yaml"]
    node = yaml.safe_load(raw)
    assert node["node"]["services"] == ["auth", "secrets", "discovery", "dns"]
    assert node["node"]["cloud"] == "test.com"
    assert node["node"]["tenancy"] == ["github.com/acme"]
    assert node["node"]["added"] == {"example": {"greeting": "hello", "replicas": 2}}
    assert "BEGIN CERTIFICATE" in node["ca"] and "BEGIN CERTIFICATE" in node["certificate"] and node["privatekey"]
    assert node["listen"] == ["/ip4/0.0.0.0/tcp/4242"]
    assert node["bootstrap"] == []  # a fleet of one bootstraps from nobody
    assert b"PRIVATE KEY" not in raw, "no domain key reaches a node: none signs a domain claim"
    # an OIDC provider reaches the node, and its secret no file: init has it on stdin
    assert node["node"]["auth"]["oidc"] == [{"name": "corp", "issuer": "https://id.test.com", "clientid": "corp-id", "scopes": ["email"]}]
    assert node["node"]["auth"]["admin"] == {"name": "root", "disabled": False}
    for secret in (b"corp-secret", b"correct-horse", b"$argon2id$"):
        assert not [path for path, data in disk.items() if secret in data]
        assert not [c for c in cmds if secret.decode() in c]

    # the sealed bundle reached init on stdin, the secret sealed in it, and no other command had stdin
    stdins = await ssh.stdins(name)
    init = 'sudo "/tb/bin/tau" "init" "--config" "/tb/config/all.yaml" "--root" "/tb/storage/all" "-"'
    bundle = stdins.pop(init)
    assert b"oidc-corp" in bundle and b"admin-password" in bundle
    assert b"corp-secret" not in bundle and b"correct-horse" not in bundle
    assert not [c for c, data in stdins.items() if data]
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
