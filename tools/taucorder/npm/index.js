#!/usr/bin/env node

const fs = require("fs");
const path = require("path");
const axios = require("axios");
const ProgressBar = require("progress");
const { spawn } = require("child_process");
const tar = require("tar");
const packageJson = require("./package.json");

const binaryDir = path.join(__dirname, "bin");
const binaryPath = path.join(binaryDir, "taucorder");
const versionFilePath = path.join(binaryDir, "version.txt");
const packageVersion = packageJson.taucorder;

function binaryExists() {
  return fs.existsSync(binaryPath);
}

function versionMatches() {
  if (!fs.existsSync(versionFilePath)) {
    return false;
  }
  const installedVersion = fs.readFileSync(versionFilePath, "utf-8").trim();
  return installedVersion === packageVersion;
}

function parseAssetName() {
  let os, arch;

  if (process.platform === "darwin") {
    os = "darwin";
  } else if (process.platform === "linux") {
    os = "linux";
  } else if (process.platform === "win32") {
    os = "windows";
  } else {
    os = null;
  }

  if (process.arch === "x64") {
    arch = "amd64";
  } else if (process.arch === "arm64") {
    arch = "arm64";
  } else {
    arch = null;
  }

  return { os, arch };
}

async function downloadAndExtractBinary() {
  if (binaryExists() && versionMatches()) {
    return;
  }

  let version = packageVersion;

  const { os: currentOs, arch: currentArch } = parseAssetName();
  const assetName = `taucorder_${version}_${currentOs}_${currentArch}.tar.gz`;
  const assetUrl = `https://github.com/taubyte/tau/releases/download/v${version}/${assetName}`;

  console.log(`Downloading taucorder v${version}...`);
  const { data, headers } = await axios({
    url: assetUrl,
    method: "GET",
    responseType: "stream",
  });

  const totalLength = headers["content-length"];
  const progressBar = new ProgressBar("-> downloading [:bar] :percent :etas", {
    width: 40,
    complete: "=",
    incomplete: " ",
    renderThrottle: 1,
    total: parseInt(totalLength),
  });

  if (!fs.existsSync(binaryDir)) {
    fs.mkdirSync(binaryDir);
  }

  const tarPath = path.join(binaryDir, assetName);
  const writer = fs.createWriteStream(tarPath);
  data.on("data", (chunk) => progressBar.tick(chunk.length));
  data.pipe(writer);

  return new Promise((resolve, reject) => {
    writer.on("finish", async () => {
      console.log(`Extracting taucorder v${version}...`);
      await tar.x({
        file: tarPath,
        C: binaryDir,
      });
      fs.unlinkSync(tarPath); // Remove the tarball after extraction
      fs.writeFileSync(versionFilePath, version); // Save the version to a file
      resolve();
    });
    writer.on("error", reject);
  });
}

function executeBinary() {
  if (!binaryExists()) {
    console.error("Binary not found. Please run the install script.");
    return;
  }

  // Capture arguments passed to the script, excluding the first two elements
  const args = process.argv.slice(2);

  const child = spawn(binaryPath, args, {
    stdio: "inherit",
  });

  child.on("error", (err) => {
    console.error("Failed to start binary:", err);
  });
}

async function main() {
  try {
    await downloadAndExtractBinary();
    executeBinary();
  } catch (err) {
    console.error(err.message);
  }
}

main();