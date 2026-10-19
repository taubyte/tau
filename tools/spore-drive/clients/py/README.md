# Spore Drive Python Client

A thin client for the spore-drive `drive.v2.DriveService`: it deploys a cloud
from its config directory (cloud.yaml, hosts.yaml, shapes.yaml, auth.yaml,
optional added.yaml and the key files they name).

```python
import asyncio
from spore_drive import Drive, read_dir, write_dir, start_service

async def main():
    port = start_service()  # downloads and runs the local spore-drive binary
    async with Drive(f"http://localhost:{port}") as drive:
        res = await drive.validate(read_dir("config"))
        if res.errors:
            raise SystemExit("\n".join(res.errors))
        # validate generates missing secrets (swarm key, domain validation
        # keys, node keys): keep them.
        write_dir(res.document, "config")

        course = await drive.plan(res.document, latest=True)
        async for p in drive.apply(course):  # leaving early aborts the course
            print(p.hop, p.host, p.step, p.progress, p.error)

asyncio.run(main())
```

- `read_dir` / `write_dir` / `from_files` move files between disk (or a dict)
  and a `Document`.
- `Drive.plan(doc, latest=|version=|url=|path=, shapes=[...], concurrency=n)`
  refuses a document Validate has not completed.
- `Drive.abort(course)` stops a planned course; an unapplied course expires on
  its own.

## Tests

```sh
venv/bin/python -m pytest tests/
```

The integration tests run the Go mock (`../mock`, needs Go), which serves
DriveService plus mock SSH hosts.
