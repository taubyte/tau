`Dreamland` is a local development tool that lets you run a full-fledged Taubyte-based Cloud on your machine. For documentation, visit [https://tau.how](https://tau.how).

## Usage

Dreamland can simulate multiple Taubyte networks, each referred to as a 'universe'. You can start a new universe (default name is "blackhole") with the following command:

```bash
$ dream new multiverse 
```
Once the message `[SUCCESS] Universe blackhole started!` appears, your local Taubyte instance is ready. You are now running elder, monkey, tns, hoarder, patrick, auth, seer, and substrate nodes locally on your machine.

You can interact with Dreamland using the Web Console. Dreamland can be selected from the network selection dropdown if it's active locally. From here, you can create new projects, import existing ones, trigger builds, and run resources like websites and dFuncs.


## Connecting Console to Dreamland
Similarly to when selecting a network on web console ([console.taubyte.com](https://console.taubyte.com)), dreamland can be selected from the network selection dropdown 
If dreamland is active, then a selection option for dreamland will appear in the network selection
![](images/web-console-login.png)


### Import a project
If your intention is to create a new project, follow the steps that you normally would if you were connected to a production cloud, by clicking `Create Project`. Note that this will create a new GitHub repo, so it is recommended that you create this as a private repo.

However, if you want to work on an existing project click on `Import Project`. This will show a menu with two dropdowns, one for your config repository, the other for your inline code repository. 
 > One convenient feature is that once selecting a config repository, if a code repository is found matching the config, it will be selected automatically.

## Branches 
If importing a project, it is recommended that you make changes to your project on a branch, rather than master/main to not affect production deployments. 
> If using Web Console Once selecting a project, you may checkout or create a new branch from the top bar.
> ![](images/web-console-branch-selector.png)


By default, ci/cd events are triggered by events on the master/main branch. You will need to override this using a fixture `setBranch`:

```bash
$ dream inject set-branch {name-of-branch}
```

## Websites and Libraries

If importing a project that has a library, or website you will need to register the resource on auth, before being able to trigger a build for these resources. This can be achieved on web console by going to the resource and using the fix repo tool.

![](../images/web-console-fix-repo.png)


## Running HTTP Resources

HTTP resources run locally on Dreamland. You need to add the domains for these resources to your `/etc/hosts` file under `127.0.0.1`. To access these resources, you also need the port that the substrate node is running on. Once you have the domain and the port, you can access the resource at `{domain}:{port}/{path}`.

## Viewing Port Information

You can view the ports that your local Tau protocols are running on using the status command.

To view a specific protocol's port (e.g., seer, auth, patrick, tns, hoarder, substrate):

```bash
$ dream status {protocol-name}
```

Example:
```bash
$ dream status substrate

@ http://127.0.0.1:11429

┌─────────────────────┬────────┬───────┐
│ substrate@blackhole │ http   │ 11429 │
│                     ├────────┼───────┤
│                     │ p2p    │ 11182 │
│                     ├────────┼───────┤
│                     │ copies │     1 │
│                     ├────────┼───────┤
│                     │ dns    │ 11204 │
└─────────────────────┴────────┴───────┘
```

To view all ports:
```bash
$ dream status universe 
```

Output will look like:
```bash
┌───────┬─────────────────────┬────────┬───────┐
│ Nodes │ elder@blackhole     │ p2p    │ 10951 │
│       ├─────────────────────┼────────┼───────┤
│       │ hoarder@blackhole   │ http   │ 10900 │
│       │                     ├────────┼───────┤
│       │                     │ p2p    │ 11042 │
│       │                     ├────────┼───────┤
│       │                     │ copies │     1 │
│       │                     ├────────┼───────┤
│       │                     │ dns    │ 11204 │
│       ├─────────────────────┼────────┼───────┤
│       │ monkey@blackhole    │ p2p    │ 11063 │
│       │                     ├────────┼───────┤
│       │                     │ copies │     1 │
│       │                     ├────────┼───────┤
│       │                     │ dns    │ 11204 │
│       │                     ├────────┼───────┤
│       │                     │ http   │ 10900 │
│       ├─────────────────────┼────────┼───────┤
│       │ tns@blackhole       │ copies │     1 │
│       │                     ├────────┼───────┤
│       │                     │ dns    │ 11204 │
│       │                     ├────────┼───────┤
│       │                     │ http   │ 11366 │
│       │                     ├────────┼───────┤
│       │                     │ p2p    │ 11126 │
│       ├─────────────────────┼────────┼───────┤
│       │ patrick@blackhole   │ copies │     1 │
│       │                     ├────────┼───────┤
│       │                     │ dns    │ 11204 │
│       │                     ├────────┼───────┤
│       │                     │ http   │ 11324 │
│       │                     ├────────┼───────┤
│       │                     │ p2p    │ 11084 │
│       ├─────────────────────┼────────┼───────┤
│       │ auth@blackhole      │ p2p    │ 11021 │
│       │                     ├────────┼───────┤
│       │                     │ copies │     1 │
│       │                     ├────────┼───────┤
│       │                     │ dns    │ 11204 │
│       │                     ├────────┼───────┤
│       │                     │ http   │ 11345 │
│       ├─────────────────────┼────────┼───────┤
│       │ seer@blackhole      │ http   │ 11303 │
│       │                     ├────────┼───────┤
│       │                     │ p2p    │ 11105 │
│       │                     ├────────┼───────┤
│       │                     │ copies │     1 │
│       │                     ├────────┼───────┤
│       │                     │ dns    │ 11204 │
│       ├─────────────────────┼────────┼───────┤
│       │ client@blackhole    │ p2p    │ 10952 │
│       ├─────────────────────┼────────┼───────┤
│       │ substrate@blackhole │ dns    │ 11204 │
│       │                     ├────────┼───────┤
│       │                     │ http   │ 11429 │
│       │                     ├────────┼───────┤
│       │                     │ p2p    │ 11182 │
│       │                     ├────────┼───────┤
│       │                     │ copies │     1 │
└───────┴─────────────────────┴────────┴───────┘
```


## Running Fixtures

Fixtures are used to inject event & data into a universe. The main fixtures you might need are `pushAll` and `attachPlugin`.

`push-all` fixture:

```bash 
$ dream inject push-all
```

`attach-plugin` fixture:

```bash
$ dream inject attach-plugin -p {path-to-plugin}
```

