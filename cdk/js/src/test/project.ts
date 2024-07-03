import { expect } from "chai";
import { open, Project } from "../project";
import * as path from "path";
import * as fs from "fs-extra";
import * as os from "os";

describe("Project", function () {
  let tempDir: string;

  beforeEach(async function () {
    tempDir = await fs.mkdtemp(path.join(os.tmpdir(), "project-"));
    const srcDir = path.resolve(__dirname, "assets");
    await fs.copy(srcDir, tempDir);
  });

  afterEach(async function () {
    await fs.remove(tempDir);
  });

  it("should open a project and return an instance of Project", async function () {
    const prj = await open(tempDir);
    console.log("Project opened successfully.");
    expect(prj).to.be.instanceOf(Project);
    await prj.close();
  });

  it("should return the project id as fakeid", async function () {
    const prj = await open(tempDir);
    const id = await prj.id;
    expect(id).to.equal("fakeid");
    await prj.close();
  });

  it("should set and return the new project id", async function () {
    const prj = await open(tempDir);
    await prj.setId("fakeid2");
    const id = await prj.id;
    expect(id).to.equal("fakeid2");
    await prj.close();
  });

  it("should return the project name as testProject", async function () {
    const prj = await open(tempDir);
    const name = await prj.name;
    expect(name).to.equal("testProject");
    await prj.close();
  });

  it("should set and return the new project name", async function () {
    const prj = await open(tempDir);
    await prj.setName("newTestProject");
    const newName = await prj.name;
    expect(newName).to.equal("newTestProject");
    await prj.close();
  });

  it("should return the project description as fakedesc", async function () {
    const prj = await open(tempDir);
    const name = await prj.description;
    expect(name).to.equal("fakedesc");
    await prj.close();
  });

  it("should set and return the new project email", async function () {
    const prj = await open(tempDir);
    await prj.setEmail("fake2@email.com");
    const newEmail = await prj.email;
    expect(newEmail).to.equal("fake2@email.com");
    await prj.close();
  });

  it("should return the project email as fake@email.com", async function () {
    const prj = await open(tempDir);
    const email = await prj.email;
    expect(email).to.equal("fake@email.com");
    await prj.close();
  });

  it("should set and return the new project description", async function () {
    const prj = await open(tempDir);
    await prj.setDescription("fakedesc2");
    const newName = await prj.description;
    expect(newName).to.equal("fakedesc2");
    await prj.close();
  });

  it("should return the project tags", async function () {
    const prj = await open(tempDir);
    const tags = await prj.tags;
    expect(tags).to.deep.equal(["tag1", "tag2"]);
    await prj.close();
  });

  it("should throw when trying to se the project tags", async function () {
    const prj = await open(tempDir);
    let error
    try {
      await prj.setTags();
    } catch (e) {
      error = e
    } finally {
      expect(error).to.be.an("Error");
    }
  });

  it("should not open a project if there's a filesystem error", async function () {
    const fakePath = path.resolve(__dirname, "fake/path");
    let error
    try {
      await open(fakePath);
      throw new Error("Expected an error to be thrown");
    } catch (e) {
      error = e
    } finally {
      expect(error).to.be.an("Error");
    }
  });
});
