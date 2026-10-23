# A function in Python: it answers the language, the method, the path and the
# body, so a caller can tell which component served it.
import wit_world
from wit_world import Request, Response


class WitWorld(wit_world.WitWorld):
    def handle(self, req: Request) -> Response:
        body = f"python {req.method} {req.path} ".encode() + bytes(req.body)
        return Response(status=200, headers=[], body=body)
