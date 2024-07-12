import createPlugin from '@extism/extism';
import * as path from 'path';

export async function core(mountPath: string): Promise<any> {
    const coreWasmPath = path.resolve(__dirname, '../core.wasm');

    return await createPlugin(
        coreWasmPath,
        {
            useWasi: true,
            allowedPaths: { '/mnt': mountPath },
        }
    );
}
