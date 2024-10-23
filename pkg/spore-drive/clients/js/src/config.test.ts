import { Config } from "./Config";
import { RPCClient } from "./ConfigClient";
import { BundleType, Source } from "../gen/config/v1/config_pb";
import { exec, ChildProcess } from "child_process";
import * as fs from "fs";
import * as path from "path";
import * as os from "os";
import { mkdtemp, rm } from "fs/promises";
import * as unzipper from "unzipper";
import * as yaml from "js-yaml";
import { Readable } from 'stream';


export const createConfig = async (config: Config) => {
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

  const withKeyAuth = config.Auth().Signer("withkey");
  await withKeyAuth.Username().Set("tau2");
  await withKeyAuth.Key().Path().Set("/keys/test.pem");

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
  const host1 = config.Hosts().Host("host1");
  await host1.Addresses().Add(["1.2.3.4/24", "4.3.2.1/24"]);
  await host1.SSH().Address().Set("1.2.3.4:4242");
  await host1.SSH().Auth().Add(["main"]);
  await host1.Location().Set("1.25, 25.1");
  await host1.Shapes().Shape("shape1").Instance().Generate();
  await host1.Shapes().Shape("shape2").Instance().Generate();

  const host2 = config.Hosts().Host("host2");
  await host2.Addresses().Add(["8.2.3.4/24", "4.3.2.8/24"]);
  await host2.SSH().Address().Set("8.2.3.4:4242");
  await host2.SSH().Auth().Add(["withkey"]);
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
  });

  afterEach(async () => {
    if (tempDir) {
      await rm(tempDir, { recursive: true, force: true });
    }
  });

  it("should set and get Cloud Domain Root", async () => {
    await config.Cloud().Domain().Root().Set("the.cloud");
    const domainRoot = await config.Cloud().Domain().Root().Get();
    expect(domainRoot).toBe("the.cloud");
  });

  it("should generate validation keys", async () => {
    await config.Cloud().Domain().Validation().Generate();

    const pathBase = config.Cloud().Domain().Validation().Keys().Path();
    expect(await pathBase.PrivateKey().Get()).toBe("keys/dv_private.key");
    expect(await pathBase.PublicKey().Get()).toBe("keys/dv_public.key");

    const dataBase = config.Cloud().Domain().Validation().Keys().Data();
    expect((await dataBase.PrivateKey().Get()).length).toBeGreaterThan(128);
    expect((await dataBase.PublicKey().Get()).length).toBeGreaterThan(128);
  });

  it("should create a valid configuration", async () => {
    await createConfig(config);

    // Verify parts of the configuration
    const rootDomain = await config.Cloud().Domain().Root().Get();
    expect(rootDomain).toBe("test.com");
    const generatedDomain = await config.Cloud().Domain().Generated().Get();
    expect(generatedDomain).toBe("gtest.com");
    const hostsList = await config.Hosts().List();
    expect(hostsList).toEqual(expect.arrayContaining(["host1", "host2"]));
  });

  it("should list hosts", async () => {
    const hostA = config.Hosts().Host("hostA");
    await hostA.Addresses().Set(["1.1.1.1", "2.2.2.1"]);
    await hostA.SSH().Address().Set("1.1.1.1:22");
    await hostA.SSH().Auth().Set(["user1"]);

    const hosts = await config.Hosts().List();
    expect(Array.isArray(hosts)).toBe(true);
    expect(hosts.length).toBe(1);
  });

  it("should commit changes", async () => {
    const result = await config.Commit();
    expect(result).toBeDefined();
  });

  it("should download configuration bundle and verify it locally and through upload", async () => {
    await createConfig(config);

    const bundleIterator = await config.Download();
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

    const config_from_zip = new Config(Readable.toWeb(fs.createReadStream(zipPath)));
    await config_from_zip.init(rpcUrl);
    expect(await config_from_zip.Cloud().Domain().Root().Get()).toBe("test.com");
    await config_from_zip.free()
  });

  it("should set and get Swarm Key", async () => {
    await config.Cloud().P2P().Swarm().Generate();
    const swarmKeyPath = await config.Cloud().P2P().Swarm().Key().Path().Get();
    expect(swarmKeyPath).toBeDefined();
    const swarmKeyData = await config.Cloud().P2P().Swarm().Key().Data().Get();
    expect(swarmKeyData.length).toBeGreaterThan(0);
  });

  it("should add, list, and delete an auth signer", async () => {
    const signer = config.Auth().Signer("testSigner");
    await signer.Username().Set("testUser");
    await signer.Password().Set("testPass");
    const signersListBeforeDelete = await config.Auth().List();
    expect(signersListBeforeDelete).toContain("testSigner");
    await signer.Delete();
    const signersListAfterDelete = await config.Auth().List();
    expect(signersListAfterDelete).not.toContain("testSigner");
  });
});
