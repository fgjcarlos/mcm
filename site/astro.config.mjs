import { defineConfig } from "astro/config";
import tailwindcss from "@tailwindcss/vite";

// Static site for fgjcarlos.github.io/mcm. Update `site` if a custom domain is wired up later.
export default defineConfig({
  site: "https://fgjcarlos.github.io",
  base: "/mcm",
  trailingSlash: "ignore",
  vite: {
    plugins: [tailwindcss()],
  },
});
