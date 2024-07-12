import { core } from './utils';

export class Project {
    private plugin: any;

    constructor(plugin: any) {
        this.plugin = plugin;
    }

    get id(): Promise<string> {
        return this.getId();
    }

    async getId(): Promise<string> {
        const out = await this.plugin.call('projectGetId');
        return out.text();
    }

    async setId(newId: string): Promise<void> {
        await this.plugin.call('projectSetId', newId);
    }

    get name(): Promise<string> {
        return this.getName();
    }

    async getName(): Promise<string> {
        const out = await this.plugin.call('projectGetName');
        return out.text();
    }

    async setName(newName: string): Promise<void> {
        await this.plugin.call('projectSetName', newName);
    }

    get description(): Promise<string> {
        return this.getDescription();
    }

    async getDescription(): Promise<string> {
        const out = await this.plugin.call('projectGetDescription');
        return out.text();
    }

    async setDescription(newDesc: string): Promise<void> {
        await this.plugin.call('projectSetDescription', newDesc);
    }

    get email(): Promise<string> {
        return this.getEmail();
    }

    async getEmail(): Promise<string> {
        const out = await this.plugin.call('projectGetEmail');
        return out.text();
    }

    async setEmail(newEmail: string): Promise<void> {
        await this.plugin.call('projectSetEmail', newEmail);
    }

    get tags(): Promise<string[]> {
        return this.getTags();
    }

    async getTags(): Promise<string[]> {
        const out = await this.plugin.call('projectGetTags');
        return out.json();
    }

    async setTags(): Promise<void> {
        await this.plugin.call('projectSetTags');
    }

    async close(): Promise<void> {
        await this.plugin.close()
    }
}

export async function open(mountPath: string): Promise<Project> {
    const plugin = await core(mountPath);

    await plugin.call('openProject');

    return new Project(plugin);
}
