const createPlugin = require("@extism/extism")

async function main() {
const plugin = await createPlugin(
	'./core.wasm',
    { 
		useWasi: true,
		// fsAccess: true,
		// supportsWasiPreview1: true,
		allowedPaths: {  '/mnt':'/home/samy/Documents/taubyte/github/tau/cdk/js/test' },
	}
);

let out = await plugin.call("t");
console.log(out.text())
}

main();