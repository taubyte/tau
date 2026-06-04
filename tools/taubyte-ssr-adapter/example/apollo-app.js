// Example: Apollo Server (GraphQL) on the Express integration, serverless-style.
//
//   npm i @apollo/server graphql express @as-integrations/express5
//   go run ./tools/taubyte-ssr-adapter --mode node --engine starlingmonkey \
//     --framework apollo --entry apollo-app.js --out handler.component.wasm
//   wasmtime serve -S cli=y handler.component.wasm
//   curl -X POST -H 'content-type: application/json' --data '{"query":"{ hello }"}' :8080/graphql
//
// Like Nest, init Apollo lazily on the first request (its start() is async) and
// hand requests to the underlying Express app. Plain JS — no build step needed.

import express from "express";
import { ApolloServer } from "@apollo/server";
import { expressMiddleware } from "@as-integrations/express5";

const typeDefs = `#graphql
  type Query { hello: String, add(a: Int!, b: Int!): Int }
`;
const resolvers = {
  Query: {
    hello: () => "Hello from Apollo on Taubyte",
    add: (_parent, { a, b }) => a + b,
  },
};

const app = express();
const apollo = new ApolloServer({ typeDefs, resolvers });

let booting = null;
function ensure() {
  return booting || (booting = (async () => {
    await apollo.start();
    app.use("/graphql", express.json(), expressMiddleware(apollo));
  })());
}

export default async function handler(req, res) {
  await ensure();
  app(req, res);
}
