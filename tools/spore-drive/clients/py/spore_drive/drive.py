"""Client for the drive.v2 DriveService.

It speaks gRPC over h2c to the connect-go service, which answers gRPC on the
same handlers.
"""

from typing import AsyncIterator, Iterable, Optional, Union

import grpc.aio

from .proto.drive.v2 import drive_pb2

_SERVICE = "/drive.v2.DriveService/"

CourseRef = Union[str, drive_pb2.Course]


def _course_id(course: CourseRef) -> str:
    return course if isinstance(course, str) else course.id


def _target(url: str) -> str:
    for prefix in ("http://", "https://"):
        if url.startswith(prefix):
            url = url[len(prefix):]
    return url.rstrip("/")


class Drive:
    """Validate, plan, apply and abort deployments of one cloud document."""

    def __init__(self, url: str):
        self.channel = grpc.aio.insecure_channel(_target(url))

    async def __aenter__(self) -> "Drive":
        return self

    async def __aexit__(self, *exc) -> None:
        await self.close()

    async def close(self) -> None:
        await self.channel.close()

    def _unary(self, method: str, resp):
        return self.channel.unary_unary(
            _SERVICE + method,
            request_serializer=lambda m: m.SerializeToString(),
            response_deserializer=resp.FromString,
        )

    async def validate(self, doc: drive_pb2.Document) -> drive_pb2.ValidateResponse:
        """Check doc. The response's document is the normalized form, with any
        missing secret generated: write it back, or the next validate
        generates different ones."""
        return await self._unary("Validate", drive_pb2.ValidateResponse)(
            drive_pb2.ValidateRequest(document=doc))

    async def plan(
        self,
        doc: drive_pb2.Document,
        *,
        latest: bool = False,
        version: Optional[str] = None,
        url: Optional[str] = None,
        path: Optional[str] = None,
        shapes: Iterable[str] = (),
        concurrency: int = 0,
    ) -> drive_pb2.Course:
        """Plan a course for a complete (validated) doc.

        At most one tau source is given: latest, version, url or path (a local
        binary). None leaves the service's default.
        """
        sources = {k: v for k, v in
                   (("latest", latest or None), ("version", version), ("url", url), ("path", path))
                   if v is not None}
        if len(sources) > 1:
            raise ValueError(f"give one tau source, got {sorted(sources)}")
        req = drive_pb2.PlanRequest(document=doc, shapes=list(shapes), concurrency=concurrency, **sources)
        return await self._unary("Plan", drive_pb2.Course)(req)

    async def apply(self, course: CourseRef) -> AsyncIterator[drive_pb2.Progress]:
        """Run course, yielding its progress. Leaving the iteration early
        aborts the course."""
        call = self.channel.unary_stream(
            _SERVICE + "Apply",
            request_serializer=lambda m: m.SerializeToString(),
            response_deserializer=drive_pb2.Progress.FromString,
        )(drive_pb2.ApplyRequest(course=_course_id(course)))
        try:
            async for p in call:
                yield p
        finally:
            call.cancel()

    async def abort(self, course: CourseRef) -> None:
        await self._unary("Abort", drive_pb2.AbortResponse)(
            drive_pb2.AbortRequest(course=_course_id(course)))
