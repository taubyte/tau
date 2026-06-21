// node:os shim — static, sandbox-appropriate values (no real machine to query).
export const EOL = "\n";
export const platform = () => "linux";
export const arch = () => "wasm32";
export const type = () => "Linux";
export const release = () => "0.0.0";
export const hostname = () => "taubyte";
export const machine = () => "wasm32";
export const cpus = () => [];
export const totalmem = () => 0;
export const freemem = () => 0;
export const loadavg = () => [0, 0, 0];
export const uptime = () => 0;
export const networkInterfaces = () => ({});
export const tmpdir = () => "/tmp";
export const homedir = () => "/";
export const endianness = () => "LE";
export const availableParallelism = () => 1;
export const userInfo = () => ({ username: "taubyte", uid: -1, gid: -1, shell: null, homedir: "/" });
export const constants = { signals: {}, errno: {}, priority: {} };
export default {
  EOL, platform, arch, type, release, hostname, machine, cpus, totalmem, freemem,
  loadavg, uptime, networkInterfaces, tmpdir, homedir, endianness, availableParallelism,
  userInfo, constants,
};
