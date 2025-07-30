# Contributing to Taubyte/Tau

Thank you for your interest in contributing to **Taubyte/Tau**, our open source distributed Platform as a Service (PaaS). Every contribution helps us build a better, more open cloud for everyone!

---

## Development Environment Setup

Before you start contributing, make sure you have the following prerequisites installed:

**Required:**
- **Go** 1.21 or later ([install guide](https://golang.org/doc/install))
- **Git** for version control
- **Node.js** 18+ and **npm** (for JavaScript/TypeScript components)
- **Docker** (needed for Dreamland testing)

For detailed setup instructions, see our [README](README.md) or visit [tau.how](https://tau.how) for comprehensive documentation.

---

## Types of Contributions We Welcome

- **Bug fixes** - Help us squash issues and improve stability
- **New features** - Add functionality that benefits the community
- **Ideas & discussions** - Share feedback, suggestions, or architectural insights

---

## Before You Start: Open an Issue

Before starting work, **please open an issue** to discuss your proposed change. This helps us avoid duplicate work and ensure your contribution aligns with the project's goals.

**How to do this:**

1. **Check for similar issues first** - Search existing [issues](https://github.com/taubyte/tau/issues) to avoid duplicates
2. Click **New Issue** and name it specifically using our format:
   - `[bug] Brief description of the bug`
   - `[feature] Brief description of the new feature`
   - `[idea] Brief description of your idea`
3. Describe your proposal clearly
4. Wait for feedback from maintainers before starting work

---

## Contribution Workflow

### 1. Fork the Repository

Click the **Fork** button at the top right of the [taubyte/tau repository](https://github.com/taubyte/tau) to create your own copy.

### 2. Clone Your Fork

On your local machine, clone your forked repository:
```bash
git clone https://github.com/<your-username>/tau.git
cd tau
```

### 3. Create a Feature Branch

GitHub will suggest a branch name when you start working on an issue. Use the suggested branch name or create one following the same format as your issue.

### 4. Make Your Changes

Implement your feature or bugfix following existing code conventions.

## 🧪 Running Automated Tests

If you are contributing to Go or JavaScript/TypeScript components, please run automated tests before pushing your changes:

**Go Tests:**

- From the root or any Go package directory:
  ```bash
  go test -p 1 ./...
  ```
  - `-p 1` runs tests serially (one package at a time).
  - `./...` runs all tests recursively in the current directory and subdirectories.

**JavaScript/TypeScript Tests:**

- From the relevant client directory (e.g., `pkg/spore-drive/clients/js` or `pkg/taucorder/clients/js`):
  ```bash
  npm run test
  ```
  - Make sure you are in the directory containing the `package.json` file for the client you want to test.

---


### 5. Commit Message Guidelines

Use the same format as issue naming:

```bash
git commit -m "[bug] fix memory leak in request handler"
git commit -m "[feature] add OAuth2 integration"
git commit -m "[dreamland] add new testing scenarios"
```

### 6. Testing

You can test your changes using **Dreamland**, our local cloud environment. See the [Dreamland documentation](https://tau.how/01-dev-getting-started/01-local-cloud/) for setup and usage details.

### 7. Push to Your Fork

Push your branch to your forked repository:
```bash
git push origin <your-branch-name>
```

### 8. Open a Pull Request

Go to the original [taubyte/tau repository](https://github.com/taubyte/tau), click **New Pull Request** and select your branch. Fill in the template describing your changes and reference related issues (e.g., `Closes #123`).

### 9. Review Process

Expect initial review within 2-3 business days. Be responsive to feedback and make requested changes by pushing to your branch.

---

## Best Practices

- **Keep pull requests focused:** One feature or fix per PR
- **Write tests** for new functionality and bug fixes
- **Be kind and inclusive:** We value respectful collaboration

---

## Community & Support

**Need Help?**
- **Discord:** Join our community at [discord.gg/zCHbgKcB](https://discord.gg/zCHbgKcB)
- **Documentation:** Visit [tau.how](https://tau.how) for comprehensive guides

---

## License

By contributing, you agree that your work will be licensed under the BSD-3-Clause license.

---

**Thank you for helping make Taubyte/Tau better!**
