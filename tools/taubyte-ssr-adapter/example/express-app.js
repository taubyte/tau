const express = require("express");

const app = express();
const PORT = process.env.PORT || 3000;

app.use(express.json());

app.get("/", (req, res) => {
  res.json({
    message: "Hello from Express v5",
    ok: true,
  });
});

app.get("/health", (req, res) => {
  res.json({
    status: "healthy",
    uptime: process.uptime(),
  });
});

app.post("/echo", (req, res) => {
  res.json({
    received: req.body,
  });
});

// Express 5 automatically forwards thrown/rejected async errors
app.get("/error", async (req, res) => {
  throw new Error("Example async error");
});

// 404 handler
app.use((req, res) => {
  res.status(404).json({
    error: "Not found",
    path: req.path,
  });
});

// Global error handler
app.use((err, req, res, next) => {
  console.error(err);

  res.status(err.status || 500).json({
    error: err.message || "Internal server error",
  });
});

app.listen(PORT, () => {
  console.log(`Express v5 server running on http://localhost:${PORT}`);
});



