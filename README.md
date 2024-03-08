<h1 align="center">
  <a href="https://taubyte.com" target="_blank" rel="noopener noreferrer">
    <picture>
      <source media="(prefers-color-scheme: dark)" srcset="images/logo-cubic-2.png">
      <img width="80" src="images/logo-light.svg" alt="Tau logo">
    </picture>
  </a>
  <br />
  Tau
</h1>
<div align="center">

[![Release](https://img.shields.io/github/release/taubyte/tau.svg)](https://github.com/taubyte/tau/releases)
[![License](https://img.shields.io/github/license/taubyte/tau)](LICENSE)
[![Go Report Card](https://goreportcard.com/badge/taubyte/tau)](https://goreportcard.com/report/taubyte/tau)
[![GoDoc](https://godoc.org/github.com/taubyte/tau?status.svg)](https://pkg.go.dev/github.com/taubyte/tau)
[![Discord](https://img.shields.io/discord/973677117722202152?color=%235865f2&label=discord)](https://discord.gg/NFhh5X3V)

</div>

<br />

**Tau** democratizes cloud computing by providing a straightforward platform for deploying serverless functions, web applications, and managing storage, making cloud services accessible for all developers. **Support Tau by turning ★ to ⭐!**


## What's Included
**Tau is a platform, so no need to build one!** As of today, this is what it's capable of:
- 🚀 Serverless WebAssembly Functions
- 🌍 Website/Frontend Hosting
- 📦 Object Storage
- 🗂 K/V Database
- 📢 Pub-Sub Messaging
- 💻 CI/CD

## 🔮 What's Next
Next, we're working to add JavaScript and Python interpreters, container support, and more. Stay engaged and [contribute](https://github.com/taubyte/tau/issues) to the future of Tau.

## 🚀 Quick Start

Getting started with Tau is as simple as 1-2-3:

1. **Install Tau**
   ```sh
   curl https://get.tau.link/tau | sh
   ```

2. **Configure**
   ```sh
   tau config generate -n yourdomain.com -s compute --protos all --ip your_public_ip
   ```

3. **Launch**
   ```sh
   tau start -s compute
   ```


## 🌌 Use Cases

- **Public Clouds**: Expand the decentralized web by hosting scalable, serverless applications.
- **Private Clouds**: Secure and control your organizational data and applications with ease.
- **Project Focus**: Eliminate infrastructure and scalability concerns, letting you concentrate on development.

## 📖 Dive Deeper

Learn more about Tau and its capabilities:
- [Introduction to Taubyte](https://taubyte.com/blog/introduction-to-taubyte/)
- [Be Competitive in a Few Minutes: Deployment Guide](https://taubyte.com/blog/be-competitive-in-few-minutes/)

For comprehensive documentation, visit our [documentation site](https://tau.how).

## 💡 Running Tau Locally

If you're focused on building your project, Dreamland ([github.com/taubyte/dreamland](https://github.com/taubyte/dreamland)) is the perfect companion. Dreamland, which utilizes libdream (see the libdream folder in this repo), allows for local development and testing, including writing E2E tests. When you're ready, you can deploy your project to any Tau-based cloud—be it public, private, or a client-specific environment. This flexibility ensures that you can concentrate on creating amazing applications with the confidence that they will work seamlessly at scale when deployed.

## 🤝 Get Involved

Join the Tau community! Whether contributing code, improving documentation, or sharing ideas, your participation is warmly welcomed.

## 📬 Need Help?

For questions or support, join our [Discord server](https://discord.gg/NFhh5X3V).

