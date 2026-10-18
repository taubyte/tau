import { Service } from "./Service";

describe("Service", () => {
  let service: Service;

  beforeAll(() => {
    service = new Service();
    service["packageVersion"] = "0.1.3"; // override to a published version
  });

  afterAll(async () => {
    await service.kill();
  });

  it("should return null when service is not running", async () => {
    await service.kill();
    const port = await service.getPort();
    expect(port).toBe(null);
  });

  // TODO: re-enable once we release a spore-drive binary that supports healthcheck
  // Need to set the right version in line 8
  it("should run the full workflow", async () => {
    console.log("Running the full workflow...");

    await service.run();

    console.log(`Binary path: ${service["binaryPath"]}`);
    expect(service["binaryExists"]()).toBe(true);

    expect(service["versionMatches"]()).toBe(true);

    const port = await service.getPort();
    console.log(`Port found: ${port}`);
    expect(typeof port).toBe("number");
  });
});
