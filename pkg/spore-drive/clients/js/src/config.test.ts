import { Config } from "./Config";
import { Bundle, BundleType } from "../gen/config/v1/config_pb";
import { exec, ChildProcess } from "child_process";
import * as fs from "fs";
import * as path from "path";
import * as os from "os";
import { mkdtemp, rm } from "fs/promises";
import * as unzipper from "unzipper";
import * as yaml from "js-yaml";
import { Readable } from "stream";

export const createConfig = async (config: Config) => {
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

  const withKeyAuth = config.auth.signer["withkey"];
  await withKeyAuth.username.set("tau2");
  await withKeyAuth.key.path.set("/keys/test.pem");

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
  const host1 = config.host["host1"];
  await host1.addresses.add(["1.2.3.4/24", "4.3.2.1/24"]);
  await host1.ssh.address.set("1.2.3.4:4242");
  await host1.ssh.auth.add(["main"]);
  await host1.location.set("1.25, 25.1");
  await host1.shape["shape1"].generate();
  await host1.shape["shape2"].generate();

  const host2 = config.host["host2"];
  await host2.addresses.add(["8.2.3.4/24", "4.3.2.8/24"]);
  await host2.ssh.address.set("8.2.3.4:4242");
  await host2.ssh.auth.add(["withkey"]);
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

export const createConfigWithSet = async (config: Config) => {
  // Set Cloud configuration
  await config.cloud.set({
    domain: {
      root: "test.com",
      generated: "gtest.com",
    },
  });
  await config.cloud.domain.validation.generate();
  await config.cloud.p2p.swarm.generate();

  // Generate P2P Swarm keys
  await config.cloud.p2p.swarm.generate();

  // Set Auth configurations
  await config.auth.set({
    main: {
      username: "tau1",
      password: "testtest",
    },
    withkey: {
      username: "tau2",
      key: "/keys/test.pem",
    },
  });

  // Set Shapes configurations
  await config.shapes.set({
    shape1: {
      services: ["auth", "seer"],
      ports: {
        main: 4242,
        lite: 4262,
      },
    },
    shape2: {
      services: ["gateway", "patrick", "monkey"],
      ports: {
        main: 6242,
        lite: 6262,
      },
      plugins: ["plugin1@v0.1"],
    },
  });

  // Set Hosts
  await config.hosts.set({
    host1: {
      addr: ["1.2.3.4/24", "4.3.2.1/24"],
      ssh: {
        addr: "1.2.3.4",
        port: 4242,
        auth: ["main"],
      },
      location: {
        lat: 1.25,
        long: 25.1,
      },
    },
    host2: {
      addr: ["8.2.3.4/24", "4.3.2.8/24"],
      ssh: {
        addr: "8.2.3.4",
        port: 4242,
        auth: ["withkey"],
      },
      location: {
        lat: 1.25,
        long: 25.1,
      },
    },
  });

  // Generate host instances key/id
  await config.host["host1"].shape["shape1"].generate();
  await config.host["host1"].shape["shape2"].generate();
  await config.host["host2"].shape["shape1"].generate();
  await config.host["host2"].shape["shape2"].generate();

  // Set P2P Bootstrap
  await config.cloud.p2p.set({
    bootstrap: {
      shape1: ["host2", "host1"],
      shape2: ["host2", "host1"],
    },
  });

  await config.commit();
};

async function extractConfigData(bundle: AsyncIterable<Bundle>): Promise<any> {
  const configData: any = {};
  const zipPath = path.join(os.tmpdir(), "config_bundle.zip");
  const writeStream = fs.createWriteStream(zipPath);

  try {
    for await (const chunk of bundle) {
      if (chunk.data?.case === "chunk") {
        writeStream.write(chunk.data.value);
      }
    }
 
    await new Promise((resolve, reject) => {
      writeStream.end();
      writeStream.on("finish", resolve);
      writeStream.on("error", reject);
    });

    const directory = await unzipper.Open.file(zipPath);
    for (const file of directory.files) {
      if (file.path.endsWith(".yaml")) {
        const content = await file.buffer();
        const yamlData = yaml.load(content.toString());
        Object.assign(configData, yamlData);
      }
    }
  } finally {
    await fs.promises.unlink(zipPath).catch(() => {});
  }

  return configData;
}

describe("Config Class Integration Tests", () => {
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
  });

  afterEach(async () => {
    if (tempDir) {
      await rm(tempDir, { recursive: true, force: true });
    }
  });

  it("should set and get Cloud Domain Root", async () => {
    await config.cloud.domain.root.set("the.cloud");
    const domainRoot = await config.cloud.domain.root.get();
    expect(domainRoot).toBe("the.cloud");
  });

  it("should generate validation keys", async () => {
    await config.cloud.domain.validation.generate();

    const pathBase = config.cloud.domain.validation.keys.path;
    expect(await pathBase.privateKey.get()).toBe("keys/dv_private.key");
    expect(await pathBase.publicKey.get()).toBe("keys/dv_public.key");

    const dataBase = config.cloud.domain.validation.keys.data;
    expect((await dataBase.privateKey.get()).length).toBeGreaterThan(128);
    expect((await dataBase.publicKey.get()).length).toBeGreaterThan(128);
  });

  it("should create a valid configuration", async () => {
    await createConfig(config);

    // Verify parts of the configuration
    const rootDomain = await config.cloud.domain.root.get();
    expect(rootDomain).toBe("test.com");
    const generatedDomain = await config.cloud.domain.generated.get();
    expect(generatedDomain).toBe("gtest.com");
    const hostsList = await config.hosts.list();
    expect(hostsList).toEqual(expect.arrayContaining(["host1", "host2"]));
  });

  it("should list hosts", async () => {
    const hostA = config.host["hostA"];
    await hostA.addresses.set(["1.1.1.1", "2.2.2.1"]);
    await hostA.ssh.address.set("1.1.1.1:22");
    await hostA.ssh.auth.set(["user1"]);

    const hosts = await config.hosts.list();
    expect(Array.isArray(hosts)).toBe(true);
    expect(hosts.length).toBe(1);
  });

  it("should commit changes", async () => {
    const result = await config.commit();
    expect(result).toBeDefined();
  });

  it("should download configuration bundle and verify it locally and through upload", async () => {
    await createConfig(config);

    const bundleIterator = await config.download();
    const zipPath = path.join(tempDir, "config_bundle.zip");
    const writeStream = fs.createWriteStream(zipPath);

    let gotType: Boolean = false;
    for await (const chunk of bundleIterator) {
      switch (chunk.data.case as string) {
        case "chunk":
          writeStream.write(chunk.data.value);
          break;
        case "type":
          expect(chunk.data.value).toBe(BundleType.BUNDLE_ZIP);
          gotType = true;
          break;
      }
    }

    writeStream.end();

    expect(gotType).toBeTruthy();

    // Wait for the write stream to finish
    await new Promise((resolve, reject) => {
      writeStream.on("finish", resolve);
      writeStream.on("error", reject);
    });

    // Ensure the file is accessible before opening
    await fs.promises.access(zipPath, fs.constants.R_OK);

    // Extract the zip file and verify the contents
    const directory = await unzipper.Open.file(zipPath);
    const yamlFile: any = directory.files.find(
      (file: unzipper.File) => file.path === "/cloud.yaml"
    );
    expect(yamlFile).toBeDefined();

    const yamlContent = await (yamlFile as unzipper.File).buffer();

    const yamlObject: any = yaml.load(yamlContent.toString());

    expect(yamlObject.domain.root).toBe("test.com");

    const config_from_zip = new Config(
      Readable.toWeb(fs.createReadStream(zipPath))
    );
    await config_from_zip.init(rpcUrl);
    expect(await config_from_zip.cloud.domain.root.get()).toBe("test.com");
    await config_from_zip.free();
  });

  it("should set and get Swarm Key", async () => {
    await config.cloud.p2p.swarm.generate();
    const swarmKeyPath = await config.cloud.p2p.swarm.key.path.get();
    expect(swarmKeyPath).toBeDefined();
    const swarmKeyData = await config.cloud.p2p.swarm.key.data.get();
    expect(swarmKeyData.length).toBeGreaterThan(0);
  });

  it("should add, list, and delete an auth signer", async () => {
    const signer = config.auth.signer["testSigner"];
    await signer.username.set("testUser");
    await signer.password.set("testPass");
    const signersListBeforeDelete = await config.auth.list();
    expect(signersListBeforeDelete).toContain("testSigner");
    await signer.delete();
    const signersListAfterDelete = await config.auth.list();
    expect(signersListAfterDelete).not.toContain("testSigner");
  });

  it("should generate same config with createConfig and createConfigWithSet", async () => {
    // Create first config using createConfig
    const config1 = new Config();
    await config1.init(rpcUrl);
    await createConfig(config1);
    const bundle1 = await config1.download();
    const config1Data = await extractConfigData(bundle1);

    // Create second config using createConfigWithSet
    const config2 = new Config();
    await config2.init(rpcUrl);
    await createConfigWithSet(config2);
    const bundle2 = await config2.download();
    const config2Data = await extractConfigData(bundle2);

    // Recursively remove id and key from any object within the configuration data
    const removeShapeIds = (obj: any) => {
      if (typeof obj !== "object" || obj === null) return;

      // Remove id and key if present
      if ("id" in obj) delete obj.id;
      if ("key" in obj) delete obj.key;

      // Recursively apply to all nested objects
      for (const key in obj) {
        if (obj.hasOwnProperty(key)) {
          removeShapeIds(obj[key]);
        }
      }
    };

    removeShapeIds(config1Data);
    removeShapeIds(config2Data);

    // Compare the configs
    expect(config1Data).toEqual(config2Data);
  });
});
