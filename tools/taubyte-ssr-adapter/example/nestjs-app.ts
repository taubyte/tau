// Example: NestJS on the Express adapter, serverless-style (lazy boot).
//
// Build with tsc (Nest's DI needs emitDecoratorMetadata, which esbuild can't
// emit), then run the emitted JS through the adapter:
//
//   # tsconfig: experimentalDecorators + emitDecoratorMetadata, module ESNext
//   tsc -p tsconfig.json                       # -> dist/nestjs-app.js
//   TAUBYTE_ESBUILD_ARGS="--external:@nestjs/microservices --external:@nestjs/websockets --external:class-transformer --external:class-validator" \
//   go run ./tools/taubyte-ssr-adapter --mode node --engine starlingmonkey \
//     --framework nestjs --entry dist/nestjs-app.js --out handler.component.wasm
//   wasmtime serve -S cli=y handler.component.wasm
//
// Key: do NOT app.listen(). Create + init Nest lazily on the first request and
// use the Express instance as the handler — the async boot then runs in the real
// event loop (the component's Wizer init snapshot can't carry a pending boot).

import "reflect-metadata";
import { NestFactory } from "@nestjs/core";
import { ExpressAdapter } from "@nestjs/platform-express";
import { Module, Controller, Get, Post, Body, Injectable } from "@nestjs/common";
import express from "express";

@Injectable()
class AppService {
  greeting(): string { return "NestJS on Taubyte"; }
}

@Controller()
class AppController {
  constructor(private readonly svc: AppService) {}
  @Get() root(): string { return `<h1>${this.svc.greeting()}</h1>`; }
  @Get("api/info") info() { return { ok: true, framework: "nestjs" }; }
  @Post("api/echo") echo(@Body() body: any) { return { echoed: body }; }
}

@Module({ controllers: [AppController], providers: [AppService] })
class AppModule {}

const server = express();
let booting: Promise<void> | null = null;
function ensure(): Promise<void> {
  return booting || (booting = (async () => {
    const app = await NestFactory.create(AppModule, new ExpressAdapter(server as any), { logger: false });
    await app.init();
  })());
}

export default async function handler(req: any, res: any): Promise<void> {
  await ensure();
  (server as any)(req, res);
}
