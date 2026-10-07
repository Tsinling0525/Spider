import { spawn } from "node:child_process";
import { existsSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { createServer, loadEnv } from "vite";

const dashboardDir = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const repoRoot = resolve(dashboardDir, "..");
const config = resolve(repoRoot, ".env.dashboard");
if (existsSync(config)) process.loadEnvFile(config);

const frontendEnv = loadEnv("development", dashboardDir, "");
// Use one credential for the managed backend and Vite's server-side proxy.
if (process.env.SPIDER_API_KEY === undefined && frontendEnv.SPIDER_API_KEY) {
  process.env.SPIDER_API_KEY = frontendEnv.SPIDER_API_KEY;
}
const listenAddress = process.env.SPIDER_DASHBOARD_LISTEN_ADDRESS || "127.0.0.1:8083";
const backendURL = new URL(frontendEnv.SPIDER_DASHBOARD_BASE_URL || `http://${listenAddress}`);
if (["0.0.0.0", "[::]"].includes(backendURL.hostname)) backendURL.hostname = "127.0.0.1";
// Pass the resolved address to Vite, including a customized backend port.
process.env.SPIDER_DASHBOARD_BASE_URL = backendURL.origin;
let backend;
let vite;
let stopping = false;
let backendFailure = "";

async function health() {
  try {
    const response = await fetch(new URL("/health", backendURL), { signal: AbortSignal.timeout(1000) });
    if (!response.ok) return false;
    const payload = await response.json();
    if (payload.service !== "dashboardd") throw new Error("配置的 dashboard 地址已被其他服务占用，请检查端口。");
    return true;
  } catch (error) {
    if (error instanceof Error && error.message.includes("其他服务")) throw error;
    return false;
  }
}

async function stop(code = 0) {
  if (stopping) return;
  stopping = true;
  await vite?.close();
  if (backend?.pid) {
    try {
      // go run creates an executable child; terminate this owned process group.
      if (process.platform === "win32") backend.kill("SIGTERM");
      else process.kill(-backend.pid, "SIGTERM");
    } catch { /* Child may have exited already. */ }
  }
  process.exit(code);
}

process.on("SIGINT", () => void stop());
process.on("SIGTERM", () => void stop());

try {
  if (!(await health())) {
    const local = ["localhost", "127.0.0.1", "[::1]"].includes(backendURL.hostname);
    const expectedPort = new URL(`http://${listenAddress}`).port;
    if (!local || backendURL.port !== expectedPort) {
      throw new Error("配置的 dashboard 服务未运行，请先启动该服务或修改 SPIDER_DASHBOARD_BASE_URL。");
    }
    console.log("正在启动 Spider dashboard 服务…");
    backend = spawn("go", ["run", "./cmd/dashboardd"], {
      cwd: repoRoot,
      env: process.env,
      stdio: "inherit",
      detached: process.platform !== "win32",
    });
    backend.on("error", () => { backendFailure = "无法启动 Go，请检查 Go 是否已安装。"; });
    backend.on("exit", (code) => {
      if (!stopping) {
        backendFailure = `dashboard 服务已退出 (${code ?? "signal"})，请检查服务日志。`;
        if (vite) { console.error(backendFailure); void stop(1); }
      }
    });
    const deadline = Date.now() + 45000;
    while (!(await health())) {
      if (backendFailure) throw new Error(backendFailure);
      if (Date.now() > deadline) throw new Error("dashboard 服务启动超时，请检查 Go 构建日志。");
      await new Promise((resolve) => setTimeout(resolve, 250));
    }
  } else {
    console.log("使用已运行的 Spider dashboard 服务。");
  }
  vite = await createServer({ root: dashboardDir, clearScreen: false, server: { host: "127.0.0.1" } });
  await vite.listen();
  vite.printUrls();
  console.log("前后端已就绪；保持此命令运行即可使用。按 Ctrl+C 关闭本次启动的服务。");
} catch (error) {
  console.error(error instanceof Error ? error.message : "dashboard 启动失败");
  await stop(1);
}
