// A function in C#: it answers the language, the method, the path and the
// body, so a caller can tell which component served it. componentize-dotnet
// generates the bindings for tau's world at build time; this class is the
// export they call.
using System.Text;
using static HandlerWorld.IHandlerWorldImports;

namespace HandlerWorld;

public class HandlerWorldExportsImpl : IHandlerWorldExports
{
    public static Response Handle(Request req)
    {
        var head = Encoding.UTF8.GetBytes($"csharp {req.method} {req.path} ");
        var body = new byte[head.Length + req.body.Length];
        head.CopyTo(body, 0);
        req.body.CopyTo(body, head.Length);
        return new Response(200, new List<Header>(), body);
    }
}
