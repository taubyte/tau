import {Config,CourseConfig,Drive,TauLatest} from "@taubyte/spore-drive";

export const createConfig = async (config: Config) => {
    // Set Cloud Domain
    await config.Cloud().Domain().Root().Set("test.com");
    await config.Cloud().Domain().Generated().Set("gtest.com");
    await config.Cloud().Domain().Validation().Generate();
    // Generate P2P Swarm keys
    await config.Cloud().P2P().Swarm().Generate();
    // Set Auth configurations
    const mainAuth = config.Auth().Signer("main");
    await mainAuth.Username().Set("tau1");
    await mainAuth.Password().Set("testtest");
    // Set Shapes configurations
    const shape1 = config.Shapes().Shape("shape1");
    await shape1.Services().Set(["auth", "seer"]);
    await shape1.Ports().Port("main").Set(BigInt(4242));
    await shape1.Ports().Port("lite").Set(BigInt(4262));
    const shape2 = config.Shapes().Shape("shape2");
    await shape2.Services().Set(["gateway", "patrick", "monkey"]);
    await shape2.Ports().Port("main").Set(BigInt(6242));
    await shape2.Ports().Port("lite").Set(BigInt(6262));
    await shape2.Plugins().Set(["plugin1@v0.1"]);
    // Set Hosts
    const host1 = config.Hosts().Host("host1");
    await host1.Addresses().Add(["1.2.3.4/24", "4.3.2.1/24"]);
    await host1.SSH().Address().Set("1.2.3.4:4242");
    await host1.SSH().Auth().Add(["main"]);
    await host1.Location().Set("1.25, 25.1");
    await host1.Shapes().Shape("shape1").Instance().Generate();
    await host1.Shapes().Shape("shape2").Instance().Generate();
    const host2 = config.Hosts().Host("host2");
    await host2.Addresses().Add(["8.2.3.4/24", "4.3.2.8/24"]);
    await host2.SSH().Address().Set("8.2.3.4:4242");
    await host2.SSH().Auth().Add(["main"]);
    await host2.Location().Set("1.25, 25.1");
    await host2.Shapes().Shape("shape1").Instance().Generate();
    await host2.Shapes().Shape("shape2").Instance().Generate();
    // Set P2P Bootstrap
    await config
        .Cloud()
        .P2P()
        .Bootstrap()
        .Shape("shape1")
        .Nodes()
        .Add(["host2", "host1"]);
    await config
        .Cloud()
        .P2P()
        .Bootstrap()
        .Shape("shape2")
        .Nodes()
        .Add(["host2", "host1"]);
    await config.Commit();
};

const config:Config= new Config()

await config.init()

await createConfig(config)

const drive:Drive = new Drive(config,TauLatest)

await drive.init()

const course = await drive.plot(new CourseConfig(["shape1"]))

await course.displace()

console.log("displacement...")
for await (const prg of await course.progress()) {
    console.log(prg)
}


console.log("done")
