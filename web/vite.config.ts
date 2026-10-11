import { defineConfig } from "vite"
import { devtools } from "@tanstack/devtools-vite"
import { tanstackStart } from "@tanstack/react-start/plugin/vite"
import viteReact from "@vitejs/plugin-react"
import tailwindcss from "@tailwindcss/vite"

const API_TARGET = process.env.API_ORIGIN ?? "http://localhost:8000"

const config = defineConfig({
  define: {
    "import.meta.env.CONTINUUM_DEMO": JSON.stringify(process.env.CONTINUUM_DEMO ?? ""),
  },
  resolve: { tsconfigPaths: true },
  plugins: [
    ...(process.env.CONTINUUM_DEMO === "1" ? [] : [devtools()]),
    tailwindcss(),
    tanstackStart({
      spa: { enabled: true },
      router: { routeFileIgnorePattern: "\\.test\\.(ts|tsx)$" },
    }),
    viteReact(),
  ],
  server: {
    proxy: {
      "/api": {
        target: API_TARGET,
        changeOrigin: true,
        rewrite: (path) => path.replace(/^\/api/, ""),
      },
      "/ws": {
        target: API_TARGET.replace(/^http/, "ws"),
        ws: true,
      },
    },
  },
})

export default config
