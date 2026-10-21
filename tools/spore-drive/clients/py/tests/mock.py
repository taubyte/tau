"""Starts the Go mock (DriveService + MockSSHService) and builds a config
directory whose hosts are mock SSH servers."""

import io
import os
import subprocess
import tarfile
import time
from typing import Dict, List, Tuple

import grpc.aio
import yaml

from spore_drive.proto.mock.v1 import ssh_pb2

MOCK_DIR = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", "mock")


def start_mock() -> Tuple[subprocess.Popen, str]:
    proc = subprocess.Popen(["go", "run", "."], cwd=MOCK_DIR, stdout=subprocess.PIPE,
                            stderr=subprocess.DEVNULL, text=True)
    line = proc.stdout.readline().strip()
    if not line.startswith("http"):
        proc.kill()
        raise RuntimeError(f"mock did not start: {line!r}")
    return proc, line


def stop_mock(proc: subprocess.Popen) -> None:
    proc.terminate()
    try:
        proc.wait(timeout=10)
    except subprocess.TimeoutExpired:
        proc.kill()
        proc.wait()


def _ser(m):
    return m.SerializeToString()


class MockSSH:
    def __init__(self, url: str):
        self.channel = grpc.aio.insecure_channel(url.split("://", 1)[-1].rstrip("/"))

    async def new_host(self, name: str, user: str = "tau", password: str = "testtest") -> int:
        hc = await self.channel.unary_unary(
            "/mock.v1.MockSSHService/New", request_serializer=_ser,
            response_deserializer=ssh_pb2.HostConfig.FromString,
        )(ssh_pb2.HostConfig(host=ssh_pb2.Host(name=name), auth_username=user, auth_password=password))
        return hc.port

    async def commands(self, name: str) -> List[str]:
        call = self.channel.unary_stream(
            "/mock.v1.MockSSHService/Commands", request_serializer=_ser,
            response_deserializer=ssh_pb2.Command.FromString,
        )(ssh_pb2.Host(name=name))
        return [c.command async for c in call]

    async def filesystem(self, name: str) -> Dict[str, bytes]:
        """The host's disk: every file it holds, by absolute path."""
        call = self.channel.unary_stream(
            "/mock.v1.MockSSHService/Filesystem", request_serializer=_ser,
            response_deserializer=ssh_pb2.BundleChunk.FromString,
        )(ssh_pb2.Host(name=name))
        bundle = b"".join([c.data async for c in call])
        with tarfile.open(fileobj=io.BytesIO(bundle)) as tar:
            return {m.name: tar.extractfile(m).read() for m in tar.getmembers() if m.isfile()}

    async def close(self):
        await self.channel.close()


def config_files(ports: Dict[str, int], added: str = None, tenancy: str = None) -> Dict[str, str]:
    """A minimal cloud: one shape, every host on it, secrets left to Validate."""
    hosts = {
        name: {
            "addr": [f"127.0.0.{i + 1}/32"],
            "ssh": {"addr": "127.0.0.1", "port": port, "auth": ["main"]},
            "shapes": {"all": {}},
        }
        for i, (name, port) in enumerate(ports.items())
    }
    files = {
        "cloud.yaml": yaml.safe_dump({
            "domain": {"root": "test.com", "generated": "g.test.com"},
            "p2p": {"bootstrap": {"all": list(ports)}},
            "auth": {"oidc": [{"name": "corp", "issuer": "https://id.test.com", "clientid": "corp-id", "clientsecret": "corp-secret"}]},
        }),
        "shapes.yaml": yaml.safe_dump({"all": {"services": ["auth", "seer"], "ports": {"main": 4242}}}),
        "hosts.yaml": yaml.safe_dump(hosts),
        "auth.yaml": yaml.safe_dump({"main": {"username": "tau", "password": "testtest"}}),
    }
    if added is not None:
        files["added.yaml"] = added
    if tenancy is not None:
        files["tenancy.yaml"] = tenancy
    return files


def fake_tau(dirpath: str) -> str:
    path = os.path.join(dirpath, "faketau")
    with open(path, "w") as f:
        f.write("#!/bin/sh\necho fake tau\n")
    os.chmod(path, 0o755)
    return path
