import Fastify from "fastify";
const app = Fastify();
app.get("/", async (req, reply) => { reply.type("text/html"); return "<h1>Fastify on Taubyte</h1>"; });
app.get("/users/:id", async (req) => ({ id: req.params.id }));
app.post("/api/echo", async (req) => ({ echoed: req.body }));
app.listen({ port: 3000 });
