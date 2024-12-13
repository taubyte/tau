import { Service } from "./Service";

describe("Service", () => {
  let service: Service;

  beforeAll(() => {
    service = new Service();
    service["packageVersion"]="0.2.0" // override to a published version
  });

  afterAll(async () => {
    await service.kill();
  });

  it("should return null when service is not running", async () => {
    const port = await service.getPort();
    expect(port).toBe(null);
  });

  it("should run the full workflow", async () => {
    console.log("Running the full workflow...");

    await service.run();

    console.log(`Binary path: ${service["binaryPath"]}`);
    expect(service["binaryExists"]()).toBe(true);

    expect(service["versionMatches"]()).toBe(true);

    await new Promise((resolve) => setTimeout(resolve, 2000));

    const port = await service.getPort();
    console.log(`Port found: ${port}`);
    expect(typeof port).toBe("number");
  }, 10000);  
});
