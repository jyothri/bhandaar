import { defineConfig, loadEnv } from "vite";
import react from "@vitejs/plugin-react-swc";
import tailwindcss from "@tailwindcss/vite";
import { tanstackRouter } from "@tanstack/router-plugin/vite";

// https://vite.dev/config/
export default defineConfig(({ mode }) => {
  // Set DEV_PUBLIC_HOST (e.g. in .env.development.local) to reach the dev
  // server through an HTTPS reverse proxy on that host. Not VITE_-prefixed,
  // so it stays out of the client bundle.
  const publicHost = loadEnv(mode, ".", "").DEV_PUBLIC_HOST;

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
