const createPlugin = require("@extism/extism")

async function main() {
const plugin = await createPlugin(
	'./core.wasm',
    { 
		useWasi: true,
		fsAccess: true,
		allowedPaths: {  '/tmp':'root' },
	}
);

let out = await plugin.call("t");
console.log(out.text())
}

main();