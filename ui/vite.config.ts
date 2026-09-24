import { defineConfig, loadEnv } from "vite";
import react from "@vitejs/plugin-react-swc";
import tailwindcss from "@tailwindcss/vite";
import { tanstackRouter } from "@tanstack/router-plugin/vite";

// Variables src/config.ts requires at runtime. Checked here too, so a build
// missing one fails (e.g. the Docker build in CI) instead of shipping a page
// that throws on load. Keep in sync with src/config.ts.
const requiredEnv = ["VITE_BACKEND_URL", "VITE_GOOGLE_CLIENT_ID"];

// https://vite.dev/config/
export default defineConfig(({ command, mode }) => {
  const env = loadEnv(mode, ".", "");

  if (command === "build") {
    const missing = requiredEnv.filter((name) => !env[name]);
    if (missing.length > 0) {
      throw new Error(
        `Missing ${missing.join(", ")} for "${mode}" build. Set in ui/.env.${mode}.`,
      );
    }
  }

  // Set DEV_PUBLIC_HOST (e.g. in .env.development.local) to reach the dev
  // server through an HTTPS reverse proxy on that host. Not VITE_-prefixed,
  // so it stays out of the client bundle.
  const publicHost = env.DEV_PUBLIC_HOST;

  return {
    plugins: [
      tanstackRouter({ target: "react", autoCodeSplitting: true }),
      react(),
      tailwindcss(),
    ],
    server: publicHost
      ? {
          host: true,
          allowedHosts: [publicHost],
          hmr: { host: publicHost, protocol: "wss", clientPort: 443 },
        }
      : undefined,
  };
});
