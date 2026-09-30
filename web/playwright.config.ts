import { defineConfig } from "@playwright/test";
export default defineConfig({ testDir: "./tests", timeout: 60000, retries: 0, workers: 1, reporter: [["list"], ["html", { open: "never" }]], use: { baseURL: "http://127.0.0.1:8787", screenshot: "only-on-failure", trace: "retain-on-failure" } });
