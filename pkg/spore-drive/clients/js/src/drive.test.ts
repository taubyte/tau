import { Config } from "./Config";
import { CourseConfig, Drive, TauBinarySource, TauPath, TauUrl } from "./Drive";
import { RPCClient as DriveClient } from "./DriveClient";
import { RPCClient as ConfigClient } from "./ConfigClient";

import { PromiseClient, createPromiseClient } from "@connectrpc/connect";
import { MockSSHService } from "../gen/mock/v1/ssh_connect";
import { createConnectTransport } from "@connectrpc/connect-node";

import { DisplacementProgress } from "../gen/drive/v1/drive_pb";

import { Source } from "../gen/config/v1/config_pb";
import { exec, ChildProcess } from "child_process";

import * as fs from "fs";
import * as path from "path";
import * as os from "os";
import { mkdtemp, rm } from "fs/promises";
import { HostConfig, Host as Hostname, Query } from "../gen/mock/v1/ssh_pb";

async function touchFile(filePath: string): Promise<void> {
  await fs.promises.writeFile(filePath, "");
}

export const createConfig = async (
  mock_client: PromiseClient<typeof MockSSHService>,
  config: Config
) => {
  // Set Cloud Domain
  await config.Cloud().Domain().Root().Set("test.com");
  await config.Cloud().Domain().Generated().Set("gtest.com");
  await config.Cloud().Domain().Validation().Generate();

  // Generate P2P Swarm keys
  await config.Cloud().P2P().Swarm().Generate();

  // Set Auth configurations
  const mainAuth = config.Auth().Signer("main");
  await mainAuth.Username().Set("tau1");
  await mainAuth.Password().Set("testtest");

  // Set Shapes configurations
  const shape1 = config.Shapes().Shape("shape1");
  await shape1.Services().Set(["auth", "seer"]);
  await shape1.Ports().Port("main").Set(BigInt(4242));
  await shape1.Ports().Port("lite").Set(BigInt(4262));

  const shape2 = config.Shapes().Shape("shape2");
  await shape2.Services().Set(["gateway", "patrick", "monkey"]);
  await shape2.Ports().Port("main").Set(BigInt(6242));
  await shape2.Ports().Port("lite").Set(BigInt(6262));
  await shape2.Plugins().Set(["plugin1@v0.1"]);

  // Set Hosts
  const host1Inst = await mock_client.new(
    new HostConfig({
      host: new Hostname({ name: "host1" }),
      authUsername: "tau1",
      authPassword: "testtest",
    })
  );

  const host1 = config.Hosts().Host("host1");
  await host1.Addresses().Add(["127.0.0.1/32"]);
  await host1
    .SSH()
    .Address()
    .Set("127.0.0.1:" + host1Inst.port);
  await host1.SSH().Auth().Add(["main"]);
  await host1.Location().Set("1.25, 25.1");
  await host1.Shapes().Shape("shape1").Instance().Generate();
  await host1.Shapes().Shape("shape2").Instance().Generate();

  const host2Inst = await mock_client.new(
    new HostConfig({
      host: new Hostname({ name: "host2" }),
      authUsername: "tau1",
      authPassword: "testtest",
    })
  );

  const host2 = config.Hosts().Host("host2");
  await host2.Addresses().Add(["127.0.0.1/32"]);
  await host2
    .SSH()
    .Address()
    .Set("127.0.0.1:" + host2Inst.port);
  await host2.SSH().Auth().Add(["main"]);
  await host2.Location().Set("1.25, 25.1");
  await host2.Shapes().Shape("shape1").Instance().Generate();
  await host2.Shapes().Shape("shape2").Instance().Generate();

  // Set P2P Bootstrap
  await config
    .Cloud()
    .P2P()
    .Bootstrap()
    .Shape("shape1")
    .Nodes()
    .Add(["host2", "host1"]);
  await config
    .Cloud()
    .P2P()
    .Bootstrap()
    .Shape("shape2")
    .Nodes()
    .Add(["host2", "host1"]);

  await config.Commit();
};

describe("Drive Class Integration Tests", () => {
  let mock_client: PromiseClient<typeof MockSSHService>;
  let drive: Drive;
  let config :Config;
  let rpcUrl: string;
  let mockServerProcess: ChildProcess;
  let tempDir: string;
  const controller = new AbortController();

  beforeAll(async () => {
    try {
      rpcUrl = await new Promise<string>((resolve, reject) => {
        mockServerProcess = exec("cd ../mock; go run .", {
          signal: controller.signal,
        });
        mockServerProcess.stdout?.on("data", (data: string) => {
          if (!rpcUrl) {
            resolve(data.trim());
          }
        });
        mockServerProcess.stderr?.on("data", (data: string) => {
          console.error("Mock server error:", data);
          reject(data);
        });
      });
    } catch (error) {
      console.error("Failed to start mock server:", error);
      throw error;
    }

    const transport = createConnectTransport({
      baseUrl: rpcUrl,
      httpVersion: "1.1",
    });

    mock_client = createPromiseClient(MockSSHService, transport);

    touchFile("/tmp/faketau")
  });

  afterAll(async () => {
    if (mockServerProcess) {
      controller.abort();
      await new Promise((resolve) => mockServerProcess.on("close", resolve));
    }
  });

  beforeEach(async () => {
    tempDir = await mkdtemp(path.join(os.tmpdir(), "cloud-")); // Create a temporary directory
    config = new Config(tempDir);
    await config.init(rpcUrl);
    await createConfig(mock_client, config);

    drive = new Drive(config, TauPath("/tmp/faketau"));
    await drive.init(rpcUrl);
  });

  afterEach(async () => {
    await mock_client.free(new Hostname({ name: "host1" }));
    await mock_client.free(new Hostname({ name: "host2" }));

    await config.free();
    await drive.free();

    if (tempDir) {
      await rm(tempDir, { recursive: true, force: true });
    }
  });

  it("should displace", async () => {
    let course = await drive.plot(new CourseConfig(["shape1"], 1));
    await course.displace();

    let deployed_hosts = new Set();
    let prgs = [];
    for await (const prg of await course.progress()) {
      const match = prg.path.match(
        /^\/[^\/]+\/(\d+\.\d+\.\d+\.\d+):(\d+)\/(.*)$/
      );
      let host: string = "";
      if (match) {
        const [_, __, port, rest] = match;
        const hconf = await mock_client.lookup(
          new Query({ eq: { case: "port", value: Number(port) } })
        );
        host = hconf.host?.name as string;
        deployed_hosts.add(host);
        prg.path = host ? `/course/${host}/${rest}` : prg.path; // Fallback to input if host is not found
      }
      if (host == "host1") prgs.push(prg);
    }

    expect(prgs).toEqual(
      expect.arrayContaining([
        new DisplacementProgress({
          path: "/course/host1/dependencies",
          name: "dependencies",
          progress: 10,
          error: "",
        }),
        new DisplacementProgress({
          path: "/course/host1/dependencies",
          name: "dependencies",
          progress: 20,
          error: "",
        }),
        new DisplacementProgress({
          path: "/course/host1/dependencies",
          name: "dependencies",
          progress: 70,
          error: "",
        }),
        new DisplacementProgress({
          path: "/course/host1/dependencies",
          name: "dependencies",
          progress: 75,
          error: "",
        }),
        new DisplacementProgress({
          path: "/course/host1/dependencies",
          name: "dependencies",
          progress: 60,
          error: "",
        }),
        new DisplacementProgress({
          path: "/course/host1/dependencies",
          name: "dependencies",
          progress: 65,
          error: "",
        }),
        new DisplacementProgress({
          path: "/course/host1/dependencies",
          name: "dependencies",
          progress: 85,
          error: "",
        }),
        new DisplacementProgress({
          path: "/course/host1/dependencies",
          name: "dependencies",
          progress: 90,
          error: "",
        }),
        new DisplacementProgress({
          path: "/course/host1/dependencies",
          name: "dependencies",
          progress: 0,
          error: "DNS resolution test failed, invalid IP: ``",
        }),
        new DisplacementProgress({
          path: "/course/host1/displacement",
          name: "displacement",
          progress: 0,
          error: "DNS resolution test failed, invalid IP: ``",
        }),
      ])
    );

    expect(deployed_hosts).toEqual(new Set(["host1", "host2"]));

    let cmds = [];
    for await (const cmd of mock_client.commands(
      new Hostname({ name: "host1" })
    )) {
      cmds.push(cmd);
    }

    expect(cmds).toEqual([
      { command: 'command "-v" "systemctl"', index: 0 },
      { command: 'command "-v" "apt"', index: 1 },
      { command: 'command "-v" "docker"', index: 2 },
      { command: 'sudo "apt-get" "update"', index: 3 },
      { command: 'command "-v" "dig"', index: 4 },
      { command: 'command "-v" "netstat"', index: 5 },
      { command: 'sudo "netstat" "-lnp"', index: 6 },
      {
        command: 'dig "+short" "+timeout=5" "@1.1.1.1" "google.com"',
        index: 7,
      },
    ]);
  });
});
