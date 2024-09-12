#!/usr/bin/env node

const fs = require("fs");
const path = require("path");
const axios = require("axios");
const ProgressBar = require("progress");
const { spawn } = require("child_process");
const tar = require("tar");
const packageJson = require("./package.json");

const binaryDir = path.join(__dirname, "bin");
const binaryPaths = {"tau-cli":path.join(binaryDir, "tau"), "dreamland": path.join(binaryDir, "dreamland")}
const tauVersion = packageJson.tau;
const dreamVersion = packageJson.dream;

function binaryExists(name) {
  return fs.existsSync(binaryPaths[name]);
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

async function downloadAndExtractBinary(name, version) {
  if (binaryExists(name)) {
    return;
  }

  const { os: currentOs, arch: currentArch } = parseAssetName();
  const assetName = `${name}_${version}_${currentOs}_${currentArch}.tar.gz`;
  const assetUrl = `https://github.com/taubyte/${name}/releases/download/v${version}/${assetName}`;

  console.log(`Downloading ${name} v${version}...`);
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
      console.log(`Extracting ${name} v${version}...`);
      await tar.x({
        file: tarPath,
        C: binaryDir,
      });
      fs.unlinkSync(tarPath); // Remove the tarball after extraction
      resolve();
    });
    writer.on("error", reject);
  });
}

function executeBinary() {
  if (!binaryExists("tau-cli")) {
    console.error("Binary not found. Please run the install script.");
    return;
  }

  // Capture arguments passed to the script, excluding the first two elements
  const args = process.argv.slice(2);

  const child = spawn(binaryPaths["tau-cli"], args, {
    stdio: "inherit",
    env: {
      ...process.env,
      DREAM_BINARY: path.join(__dirname, binaryPaths["dreamland"]) // Replace with your environment variable and value
  }
  });

  child.on("error", (err) => {
    console.error("Failed to start binary:", err);
  });
}

async function main() {
  try {
    await downloadAndExtractBinary("tau-cli",tauVersion);
    await downloadAndExtractBinary("dreamland", dreamVersion);
    executeBinary();
  } catch (err) {
    console.error(err.message);
  }
}

main();
