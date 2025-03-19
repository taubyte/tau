import { Config } from "./Config";
import { CourseConfig, Drive, TauPath } from "./Drive";

import { PromiseClient, createPromiseClient } from "@connectrpc/connect";
import { MockSSHService } from "../gen/mock/v1/ssh_connect";
import { createConnectTransport } from "@connectrpc/connect-node";

import { DisplacementProgress } from "../gen/drive/v1/drive_pb";

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
  await config.cloud.domain.root.set("test.com");
  await config.cloud.domain.generated.set("gtest.com");
  await config.cloud.domain.validation.generate();

  // Generate P2P Swarm keys
  await config.cloud.p2p.swarm.generate();

  // Set Auth configurations
  const mainAuth = config.auth.signer["main"];
  await mainAuth.username.set("tau1");
  await mainAuth.password.set("testtest");

  // Set Shapes configurations
  const shape1 = config.shape["shape1"];
  await shape1.services.set(["auth", "seer"]);
  await shape1.ports.port["main"].set(4242);
  await shape1.ports.port["lite"].set(4262);

  const shape2 = config.shape["shape2"];
  await shape2.services.set(["gateway", "patrick", "monkey"]);
  await shape2.ports.port["main"].set(6242);
  await shape2.ports.port["lite"].set(6262);
  await shape2.plugins.set(["plugin1@v0.1"]);

  // Set Hosts
  const host1Inst = await mock_client.new(
    new HostConfig({
      host: new Hostname({ name: "host1" }),
      authUsername: "tau1",
      authPassword: "testtest",
    })
  );

  const host1 = config.host["host1"];
  await host1.addresses.add(["127.0.0.1/32"]);
  await host1.ssh.address.set("127.0.0.1:" + host1Inst.port);
  await host1.ssh.auth.add(["main"]);
  await host1.location.set("1.25, 25.1");
  await host1.shape["shape1"].generate();
  await host1.shape["shape2"].generate();

  const host2Inst = await mock_client.new(
    new HostConfig({
      host: new Hostname({ name: "host2" }),
      authUsername: "tau1",
      authPassword: "testtest",
    })
  );

  const host2 = config.host["host2"];
  await host2.addresses.add(["127.0.0.1/32"]);
  await host2.ssh.address.set("127.0.0.1:" + host2Inst.port);
  await host2.ssh.auth.add(["main"]);
  await host2.location.set("1.25, 25.1");
  await host2.shape["shape1"].generate();
  await host2.shape["shape2"].generate();

  // Set P2P Bootstrap
  await config.cloud.p2p.bootstrap.shape["shape1"].nodes.add([
    "host2",
    "host1",
  ]);
  await config.cloud.p2p.bootstrap.shape["shape2"].nodes.add([
    "host2",
    "host1",
  ]);

  await config.commit();
};

describe("Drive Class Integration Tests", () => {
  let mock_client: PromiseClient<typeof MockSSHService>;
  let drive: Drive;
  let config: Config;
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
            // Wait 3 seconds before resolving to ensure server is ready
            setTimeout(() => {
              resolve(data.trim());
            }, 3000);
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

    touchFile("/tmp/faketau");
  });

  afterAll(async () => {
    if (mockServerProcess) {
      await new Promise((resolve) => setTimeout(resolve, 1000));
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

    expect(new Set(cmds)).toEqual(
      new Set([
        { command: 'command "-v" "systemctl"', index: expect.any(Number) },
        { command: 'command "-v" "apt"', index: expect.any(Number) },
        { command: 'command "-v" "docker"', index: expect.any(Number) },
        { command: 'sudo "apt-get" "update"', index: expect.any(Number) },
        { command: 'command "-v" "dig"', index: expect.any(Number) },
        { command: 'command "-v" "netstat"', index: expect.any(Number) },
        { command: 'sudo "netstat" "-lnp"', index: expect.any(Number) },
        {
          command: 'dig "+short" "+timeout=5" "@1.1.1.1" "google.com"',
          index: expect.any(Number),
        },
      ])
    );
    expect(cmds.length).toBe(8);
  });
});
