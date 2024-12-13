import { exec, ChildProcess } from "child_process";
import { Taucorder, TaucorderService } from "./Taucorder";
import { Config } from "../gen/taucorder/v1/node_pb";
import { Peer } from "../gen/taucorder/v1/common_pb";

describe("Taucorder test", () => {
  let taucorder: Taucorder;
  let mockServerProcess: ChildProcess;
  let universeConfig: any;
  const uname = "mock_universe";
  const controller = new AbortController();

  beforeAll(async () => {
    try {
      await new Promise<void>((resolve, reject) => {
        mockServerProcess = exec("cd ../mock; go run .", {
          signal: controller.signal,
        });
        mockServerProcess.stdout?.on("data", (data: string) => {
          if (!universeConfig) {
            const match = data.match(/@@(.*?)@@/);
            if (match) {
              universeConfig = JSON.parse(match[1]);
              resolve();
            }
          }
        });
        mockServerProcess.stderr?.on("data", (data: string) => {
          console.log("Mock server error:", data);
          reject(data);
        });
      });
    } catch (error) {
      console.error("Failed to start mock server:", error);
      throw error;
    }

    let tch = new TaucorderService(universeConfig.url);
    await tch.wait(10);

    taucorder = new Taucorder(
      universeConfig.url,
      new Config({
        source: {
          case: "universe",
          value: {
            universe: uname,
          },
        },
      })
    );
    await taucorder.init();
  }, 30000);

  it("should create a Taucorder instance", () => {
    expect(taucorder).toBeDefined();
    expect(taucorder).toBeInstanceOf(Taucorder);
  });
  it("should initialize Auth service", () => {
    const auth = taucorder.Auth();
    expect(auth).toBeDefined();
    expect(auth.Projects).toBeDefined();
    expect(auth.GitHooks).toBeDefined();
    expect(auth.Repositories).toBeDefined();
    expect(auth.X509).toBeDefined();
  });

  it("should initialize Monkey service", () => {
    const monkey = taucorder.Monkey();
    expect(monkey).toBeDefined();
  });

  it("should initialize Hoarder service", () => {
    const hoarder = taucorder.Hoarder();
    expect(hoarder).toBeDefined();
  });

  it("should initialize Patrick service", () => {
    const patrick = taucorder.Patrick();
    expect(patrick).toBeDefined();
  });

  it("should initialize Seer service", () => {
    const seer = taucorder.Seer();
    expect(seer).toBeDefined();
  });

  it("should initialize Swarm service", () => {
    const swarm = taucorder.Swarm();
    expect(swarm).toBeDefined();
  });

  it("should initialize TNS service", () => {
    const tns = taucorder.TNS();
    expect(tns).toBeDefined();
  });

  it("should call TNS list method", async () => {
    const tns = taucorder.TNS();
    const paths = tns.list(2);
    expect(paths).toBeDefined();

    // Test the async iteration
    for await (const path of await paths) {
      expect(Array.isArray(path)).toBe(true);
      expect(path.every((leaf) => typeof leaf === "string")).toBe(true);
    }
  });

  it("should call TNS fetch method", async () => {
    const tns = taucorder.TNS();
    const path = ["test", "path"];
    const result = await tns.fetch(path);
    expect(typeof result).toBe("string");
  });

  it("should call TNS lookup method with prefix", async () => {
    const tns = taucorder.TNS();
    const path = ["test", "prefix"];
    const results = await tns.lookup(path, "prefix");
    expect(Array.isArray(results)).toBe(true);
    results.forEach((path) => {
      expect(Array.isArray(path)).toBe(true);
      expect(path.every((leaf) => typeof leaf === "string")).toBe(true);
    });
  });

  it("should call TNS lookup method with regex", async () => {
    const tns = taucorder.TNS();
    const path = ["test", ".*"];
    const results = await tns.lookup(path, "regex");
    expect(Array.isArray(results)).toBe(true);
    results.forEach((path) => {
      expect(Array.isArray(path)).toBe(true);
      expect(path.every((leaf) => typeof leaf === "string")).toBe(true);
    });
  });

  it("should call TNS state method", async () => {
    const tns = taucorder.TNS();
    const peerId = universeConfig.nodes.find(
      (node: any) => node.name === "tns@mock_universe"
    )?.id;
    const state = await tns.state(peerId);
    expect(state).toBeDefined();
  });

  it("should call TNS states method", async () => {
    const tns = taucorder.TNS();
    const states = await tns.states();
    expect(states).toBeDefined();

    // Test the async iteration
    for await (const state of states) {
      expect(state).toBeDefined();
    }
  });

  it("should throw error when accessing services before initialization", () => {
    const uninitializedTaucorder = new Taucorder(
      universeConfig.url,
      new Config({
        source: {
          case: "universe",
          value: {
            universe: uname,
          },
        },
      })
    );

    expect(() => uninitializedTaucorder.Auth()).toThrow("Node not initialized");
    expect(() => uninitializedTaucorder.Monkey()).toThrow(
      "Node not initialized"
    );
    expect(() => uninitializedTaucorder.Hoarder()).toThrow(
      "Node not initialized"
    );
    expect(() => uninitializedTaucorder.TNS()).toThrow("Node not initialized");
  });

  it("should cache service instances", () => {
    const auth1 = taucorder.Auth();
    const auth2 = taucorder.Auth();
    expect(auth1).toBe(auth2);

    const monkey1 = taucorder.Monkey();
    const monkey2 = taucorder.Monkey();
    expect(monkey1).toBe(monkey2);
  });

  it("should call all Auth methods", async () => {
    const auth = taucorder.Auth();

    // Test list
    const peers = auth.list();
    let firstPeer: undefined | Peer;
    for await (const peer of await peers) {
      if (!firstPeer) {
        firstPeer = peer;
      }
      expect(peer).toBeDefined();
    }

    // Test discover
    const discovered = auth.discover(5000, 10);
    for await (const peer of await discovered) {
      expect(peer).toBeDefined();
    }

    // Test state
    if (!firstPeer) {
      throw new Error("No peers found");
    }
    const state = await auth.state(firstPeer.id);
    expect(state).toBeDefined();

    // Test states
    const states = auth.states();
    for await (const state of await states) {
      expect(state).toBeDefined();
    }
  });

  it("should call all GitHooks methods", async () => {
    const gitHooks = taucorder.Auth().GitHooks();

    // Test list
    const hooks = gitHooks.list();
    for await (const hook of await hooks) {
      expect(hook).toBeDefined();
    }

    // Test get
    await expect(async () => {
      await gitHooks.get("test-hook-id");
    }).rejects.toThrow("datastore: key not found");
  });

  it("should call all Hoarder methods", async () => {
    const hoarder = taucorder.Hoarder();

    // Test list
    const items = hoarder.list();
    for await (const item of await items) {
      expect(item).toBeDefined();
    }

    // Test stash
    const response = await hoarder.stash("test-cid");
    expect(response).toBeDefined();
  });

  it("should call all Monkey methods", async () => {
    const monkey = taucorder.Monkey();

    // Test list
    const jobs = monkey.list();
    for await (const job of await jobs) {
      expect(job).toBeDefined();
    }

    // Test get
    await expect(async () => {
      await monkey.get("test-job-id");
    }).rejects.toThrow(/job `test-job-id` not found/);
  });

  it("should call all Patrick methods", async () => {
    const patrick = taucorder.Patrick();

    // Test list
    const jobs = patrick.list();
    for await (const job of await jobs) {
      expect(job).toBeDefined();
    }

    // Test get
    await expect(async () => {
      await patrick.get("test-job-id");
    }).rejects.toThrow(/could not find test-job-id/);

    // Test state
    const peerId = universeConfig.nodes.find(
      (node: any) => node.name === "patrick@mock_universe"
    )?.id;
    const state = await patrick.state(peerId);
    expect(state).toBeDefined();

    // Test states
    const states = patrick.states();
    for await (const state of await states) {
      expect(state).toBeDefined();
    }
  });

  it("should call all Projects methods", async () => {
    const projects = taucorder.Auth().Projects();

    // Test list
    const projectsList = projects.list();
    for await (const project of await projectsList) {
      expect(project).toBeDefined();
    }

    // Test get
    await expect(async () => {
      await projects.get("test-project-id");
    }).rejects.toThrow(/can't fetch project `test-project-id`/);
  });

  it("should call all Repositories methods", async () => {
    const repos = taucorder.Auth().Repositories();

    // Test list
    const reposList = repos.list();
    for await (const repo of await reposList) {
      expect(repo).toBeDefined();
    }

    // Test get
    await expect(async () => {
      await repos.get("123456"); // GitHub repo ID
    }).rejects.toThrow(/`123456` does not exist/);
  });

  it("should call all Seer methods", async () => {
    const seer = taucorder.Seer();

    // Test list
    const peers = seer.list("auth");
    for await (const peer of await peers) {
      expect(peer).toBeDefined();
    }
  });

  it("should call all Swarm methods", async () => {
    const swarm = taucorder.Swarm();

    // Test wait
    const waitResponse = await swarm.wait(BigInt(5000000000)); // 5 seconds
    expect(waitResponse).toBeDefined();

    // Test list
    const peers = swarm.list({
      timeout_seconds: 5,
      ping: { count: 3, concurrency: 2 },
    });
    for await (const peer of await peers) {
      expect(peer).toBeDefined();
    }

    // Test ping
    const peer = universeConfig.nodes.find(
      (node: any) => node.name === "seer@mock_universe"
    );
    const pingResult = await swarm.ping(peer.id, {
      timeout_seconds: 5,
      count: 3,
    });
    expect(pingResult).toBeDefined();

    // Test connect
    const connectedPeer = await swarm.connect(
      `/ip4/127.0.0.1/tcp/${peer.value.p2p}/p2p/${peer.id}`,
      BigInt(5000000000)
    );
    expect(connectedPeer).toBeDefined();

    // Test discover
    const discovered = swarm.discover("auth", {
      timeout_seconds: 5,
      count: 10,
    });
    for await (const peer of await discovered) {
      expect(peer).toBeDefined();
    }
  });

  it("should call all X509 methods", async () => {
    const x509 = taucorder.Auth().X509();

    // Test list
    await expect(async () => {
      const certs = await x509.list();
      for await (const cert of certs) {
        expect(cert).toBeDefined();
      }
    }).rejects.toThrow(/not implemented/);

    // Test set
    const setResponse = await x509.set(
      "test.domain.com",
      new Uint8Array([1, 2, 3])
    );
    expect(setResponse).toBeDefined();

    // Test get
    const cert = await x509.get("test.domain.com");
    expect(cert).toBeDefined();

    // Test delete
    await expect(async () => {
      await x509.delete("test.domain.com");
    }).rejects.toThrow(/not implemented/);
  });

  afterAll(async () => {
    if (mockServerProcess) {
      controller.abort();
      await new Promise((resolve) => mockServerProcess.on("close", resolve));
    }
  });
});
