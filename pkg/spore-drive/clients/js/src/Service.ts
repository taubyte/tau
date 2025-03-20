import fs from "fs";
import path from "path";
import axios from "axios";
import cliProgress from "cli-progress";
import { spawn } from "child_process";
import * as tar from "tar";
import packageJson from "../package.json";
import { homedir, platform } from "os";
import * as os from "os";

import { Health } from "./Health";

import { createConnectTransport } from "@connectrpc/connect-node";

import { fileURLToPath } from "url";
const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

interface RunFile {
  port: number;
  pid: number;
}

export class Service {
  private binaryDir: string;
  private binaryPath: string;
  private versionFilePath: string;
  private packageVersion: string;
  private runFilePath: string;
  private binaryName: string;

  constructor() {
    this.binaryDir = path.join(__dirname, "bin");
    this.binaryName = os.platform() === "win32" ? "drive.exe" : "drive";
    this.binaryPath = path.join(this.binaryDir, this.binaryName);
    this.versionFilePath = path.join(this.binaryDir, "version.txt");
    this.packageVersion = packageJson.service;
    this.runFilePath = path.join(this.getConfigDir(), ".spore-drive.run");
  }

  private getConfigDir(): string {
    const plt = platform();
    if (plt === "win32") {
      return process.env.APPDATA || path.join(homedir(), "AppData", "Roaming");
    } else if (plt === "darwin") {
      return path.join(homedir(), "Library", "Application Support");
    } else {
      return process.env.XDG_CONFIG_HOME || path.join(homedir(), ".config");
    }
  }

  private binaryExists(): boolean {
    return fs.existsSync(this.binaryPath);
  }

  private versionMatches(): boolean {
    if (!fs.existsSync(this.versionFilePath)) {
      return false;
    }
    const installedVersion = fs
      .readFileSync(this.versionFilePath, "utf-8")
      .trim();
    return installedVersion === this.packageVersion;
  }

  private parseAssetName(): { os: string | null; arch: string | null } {
    let os: string | null = null;
    let arch: string | null = null;

    switch (process.platform) {
      case "darwin":
        os = "darwin";
        break;
      case "linux":
        os = "linux";
        break;
      case "win32":
        os = "windows";
        break;
    }

    switch (process.arch) {
      case "x64":
        arch = "amd64";
        break;
      case "arm64":
        arch = "arm64";
        break;
    }

    return { os, arch };
  }

  private loadRunFile(): RunFile | null {
    if (fs.existsSync(this.runFilePath)) {
      const runData = fs.readFileSync(this.runFilePath, "utf-8");
      const runFile: RunFile = JSON.parse(runData);
      return runFile;
    }
    return null;
  }

  private isProcessRunning(pid: number): boolean {
    try {
      process.kill(pid, 0);
      return true;
    } catch (e) {
      return false;
    }
  }

  private async isServiceUp(port: number): Promise<boolean> {
    const transport = createConnectTransport({
      baseUrl: `http://localhost:${port}`,
      httpVersion: "1.1",
    });
    try {
      const hc = new Health(transport);
      await hc.ping();
      return true;
    } catch (e) {
      console.log("Service is not up on port", port, e);
      return false;
    }
  }

  private async downloadAndExtractBinary(): Promise<void> {
    if (this.binaryExists() && this.versionMatches()) {
      return;
    }

    const version = this.packageVersion;
    const { os: currentOs, arch: currentArch } = this.parseAssetName();

    if (!currentOs || !currentArch) {
      throw new Error("Unsupported OS or architecture");
    }

    const assetName = `spore-drive-service_${version}_${currentOs}_${currentArch}.tar.gz`;
    const assetUrl = `https://github.com/taubyte/spore-drive/releases/download/v${version}/${assetName}`;

    console.log(
      `Downloading spore-drive service v${version} for ${currentOs}/${currentArch}...`
    );

    const { data, headers } = await axios({
      url: assetUrl,
      method: "GET",
      responseType: "stream",
    });

    const totalLength = parseInt(headers["content-length"] || "0", 10);

    const progressBar = new cliProgress.SingleBar(
      {
        format: "Progress |{bar}| {percentage}% || {value}/{total} Chunks",
        barCompleteChar: "\u2588",
        barIncompleteChar: "\u2591",
        hideCursor: true,
      },
      cliProgress.Presets.shades_classic
    );

    progressBar.start(totalLength, 0);

    let downloadedLength = 0;

    if (!fs.existsSync(this.binaryDir)) {
      fs.mkdirSync(this.binaryDir);
    }

    const tarPath = path.join(this.binaryDir, assetName);
    const writer = fs.createWriteStream(tarPath);

    data.on("data", (chunk: any) => {
      downloadedLength += chunk.length;
      progressBar.update(downloadedLength);
    });

    data.pipe(writer);

    return new Promise((resolve, reject) => {
      writer.on("finish", async () => {
        progressBar.stop();
        console.log(`\nExtracting spore-drive service v${version}...`);
        await tar.x({
          file: tarPath,
          C: this.binaryDir,
        });
        fs.unlinkSync(tarPath);
        fs.writeFileSync(this.versionFilePath, version);
        resolve();
      });
      writer.on("error", (err) => {
        progressBar.stop();
        reject(err);
      });
    });
  }

  public async getPort(timeout: number = 3500): Promise<number | null> {
    const startTime = Date.now();

    while (Date.now() - startTime < timeout) {
      const runFile = this.loadRunFile();
      if (
        runFile &&
        this.isProcessRunning(runFile.pid) &&
        (await this.isServiceUp(runFile.port))
      ) {
        return runFile.port;
      }
      await new Promise((resolve) => setTimeout(resolve, 500));
    }

    return null;
  }

  private async executeBinary(): Promise<void> {
    if (!this.binaryExists()) {
      console.error("Binary not found. Please run the install script.");
      return;
    }

    return new Promise<void>((resolve, reject) => {
      const child = spawn(this.binaryPath, process.argv.slice(2), {
        detached: true,
        stdio: "ignore",
      });

      child.unref();

      child.on("error", (err) => {
        reject(err);
      });

      child.on("spawn", () => {
        resolve();
      });

      child.on("exit", (code, signal) => {
        if (code !== 0) {
          reject(new Error(`Binary exited with code ${code}`));
        }
      });
    });
  }

  public async kill(): Promise<void> {
    const runFile = this.loadRunFile();
    if (runFile && this.isProcessRunning(runFile.pid)) {
      try {
        await new Promise<void>((resolve, reject) => {
          try {
            process.kill(runFile.pid);
            fs.unlinkSync(this.runFilePath);
            console.log(
              `Service running on port ${runFile.port} has been stopped.`
            );
            resolve();
          } catch (error: any) {
            console.error(`Failed to stop service: ${error.message}`);
            reject(error);
          }
        });
      } catch (error) {
        console.error("Error during service shutdown:", error);
      }
    } else {
      console.log("No running service found.");
    }
  }

  public async run(): Promise<void> {
    let port = await this.getPort();
    if (port === null) {
      await this.downloadAndExtractBinary();
      await this.executeBinary();
      port = await this.getPort();
      if (port === null) {
        throw new Error("Failed to start service");
      }
    }
  }
}
