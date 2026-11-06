import { Service } from "./Service";

// The service binary comes from taubyte/spore-drive releases unless a fork's
// releases are named; neither override changes anything when unset.
class Exposed extends Service {
  url(version: string) {
    return this.releaseAssetUrl(version, "linux", "amd64");
  }
  version() {
    return (this as any).packageVersion as string;
  }
}

describe("Service release source", () => {
  const saved = { ...process.env };
  afterEach(() => {
    process.env = { ...saved };
  });

  it("downloads from taubyte/spore-drive by default", () => {
    delete process.env.SPORE_DRIVE_RELEASES;
    expect(new Exposed().url("0.1.6")).toBe(
      "https://github.com/taubyte/spore-drive/releases/download/v0.1.6/spore-drive-service_0.1.6_linux_amd64.tar.gz",
    );
  });

  it("downloads from the named repository's releases", () => {
    process.env.SPORE_DRIVE_RELEASES = "someone/spore-drive";
    expect(new Exposed().url("0.1.7")).toBe(
      "https://github.com/someone/spore-drive/releases/download/v0.1.7/spore-drive-service_0.1.7_linux_amd64.tar.gz",
    );
  });

  it("uses the named tag prefix", () => {
    process.env.SPORE_DRIVE_RELEASES = "someone/tau";
    process.env.SPORE_DRIVE_TAG_PREFIX = "spore-drive-v";
    expect(new Exposed().url("0.1.7")).toBe(
      "https://github.com/someone/tau/releases/download/spore-drive-v0.1.7/spore-drive-service_0.1.7_linux_amd64.tar.gz",
    );
  });

  it("runs the named service version", () => {
    process.env.SPORE_DRIVE_SERVICE_VERSION = "0.1.7";
    expect(new Exposed().version()).toBe("0.1.7");
    delete process.env.SPORE_DRIVE_SERVICE_VERSION;
    expect(new Exposed().version()).not.toBe("0.1.7");
  });
});
