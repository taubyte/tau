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

Or load a configuration from a source, for example, the local file system:
```ts
const config = new Config(`/absolute/path/to/config`);
await config.init();
```

### Define Cloud Infrastructure

You can define your cloud infrastructure as follows:
```ts
const cloudDomain = await config.Cloud().Domain();
await cloudDomain.Root().Set("test.com");
await cloudDomain.Generated().Set("gtest.com");
await cloudDomain.Validation().Generate();
```

For a complete example, see the `example` folder in the `pkg/spore-drive` directory.

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

function extractHost(path: string): string {
  const match = path.match(/\/([^\/]+):\d+/);
  return match ? match[1] : "unknown-host";
}

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