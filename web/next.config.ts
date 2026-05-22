import type { NextConfig } from "next";
import path from "path";

const nextConfig: NextConfig = {
  output: "standalone",
  turbopack: {
    // Explicitly anchor the project root so Turbopack never walks up past web/
    // and accidentally picks up /Users/<user>/package.json or the repo root.
    root: path.resolve(__dirname),
  },
};

export default nextConfig;
