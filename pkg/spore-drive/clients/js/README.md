# @taubyte/spore-drive

Gone are the days of relying on third parties to build and maintain cloud infrastructure for your software. **spore-drive** empowers you to deploy, scale, and manage your [Tau](https://github.com/taubyte/tau) cloud infrastructure with code!

<img src="https://github.com/taubyte/tau/raw/main/pkg/spore-drive/clients/js/assets/preview.gif"/>

## Installation

### npm
```sh
npm install @taubyte/spore-drive
```

### yarn
```sh
yarn add @taubyte/spore-drive
```

## Example Usage

### Load Configuration

Create a new configuration in memory:
```ts
import { Config } from "@taubyte/spore-drive";

const config = new Config();
await config.init();
```

Alternatively, load a configuration from a source, such as the local file system:
```ts
const config = new Config(`/absolute/path/to/config`);
await config.init();
```

### Define Cloud Infrastructure

You can define your cloud infrastructure as follows:
```ts
await config.cloud.set({
  domain: {
    root: "test.com",
    generated: "gtest.com",
  },
});
await config.cloud.domain.validation.generate();
await config.cloud.p2p.swarm.generate();
```

Or for more granular control:
```ts
await config.cloud.domain.root.set("test.com");
await config.cloud.domain.generated.set("gtest.com");
await config.cloud.domain.validation.generate();
await config.cloud.p2p.swarm.generate();
```

### Set Auth Configurations
```ts
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
```

Or
```ts
const mainAuth = config.auth.signer["main"];
await mainAuth.username.set("tau1");
await mainAuth.password.set("testtest");

const withKeyAuth = config.auth.signer["withkey"];
await withKeyAuth.username.set("tau2");
await withKeyAuth.key.path.set("/keys/test.pem");
```

### Set Shapes Configurations
```ts
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
```

Or
```ts
const shape1 = config.shape["shape1"];
await shape1.services.set(["auth", "seer"]);
await shape1.ports.port["main"].set(4242);
await shape1.ports.port["lite"].set(4262);
```

### Set Hosts
```ts
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
```

Or
```ts
const host1 = config.host["host1"];
await host1.addresses.add(["1.2.3.4/24", "4.3.2.1/24"]);
await host1.ssh.address.set("1.2.3.4:4242");
await host1.ssh.auth.add(["main"]);
await host1.location.set("1.25, 25.1");
await host1.shape["shape1"].generate();
await host1.shape["shape2"].generate();
```


### Set P2P Bootstrap

```ts
await config.cloud.p2p.set({
  bootstrap: {
    shape1: ["host2", "host1"],
    shape2: ["host2", "host1"],
  },
});
```

Or
```ts
await config.cloud.p2p.bootstrap.shape["shape1"].nodes.add([
  "host2",
  "host1",
]);
```


### Instantiate a Drive

```ts
import { Drive, TauLatest } from "@taubyte/spore-drive";

const drive = new Drive(config, TauLatest);
await drive.init();
```

### Plot a Course

```ts
const course = await drive.plot(new CourseConfig(["shape1"]));
```

`shape1` is used to select hosts to be deployed to.

### Deploy

```ts
await course.displace();

console.log("Displacing...");
for await (const progress of await course.progress()) {
    console.log(progress);
}
console.log("Done");
```

### Progress Bars

You can visualize the deployment progress using progress bars:

```ts
import { ProgressBar } from "@opentf/cli-pbar";

// Extracts the host from the given path
function extractHost(path: string): string {
  const match = path.match(/\/([^\/]+):\d+/);
  return match ? match[1] : "unknown-host";
}

// Extracts the task from the given path
function extractTask(path: string): string {
  const parts = path.split("/");
  return parts[parts.length - 1] || "unknown-task";
}

async function displayProgress(course: Course) {
  const multiPBar = new ProgressBar({ size: "SMALL" });
  multiPBar.start();
  const taskBars: Record<string, any> = {};
  const errors: { host: string; task: string; error: string }[] = [];

  for await (const displacement of await course.progress()) {
    const host = extractHost(displacement.path);
    const task = extractTask(displacement.path);

    if (!taskBars[host]) {
      taskBars[host] = multiPBar.add({
        prefix: host,
        suffix: "...",
        total: 100,
      });
    }

    taskBars[host].update({ value: displacement.progress, suffix: task });

    if (displacement.error) {
      errors.push({ host, task, error: displacement.error });
    }
  }

  for (const host in taskBars) {
    const errorForHost = errors.find((err) => err.host === host);

    if (errorForHost) {
      taskBars[host].update({ value: 100, color: "r", suffix: "failed" });
    } else {
      taskBars[host].update({ value: 100, suffix: "successful" });
    }
  }

  multiPBar.stop();

  if (errors.length > 0) {
    console.log("\nErrors encountered:");
    errors.forEach((err) => {
      console.log(`Host: ${err.host}, Task: ${err.task}, Error: ${err.error}`);
    });
  }
}
```
