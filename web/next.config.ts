import type { NextConfig } from "next";
import path from "path";

const backendURL = process.env.BACKEND_URL ?? "http://localhost:8080";

const nextConfig: NextConfig = {
  output: "standalone",
  turbopack: {
    // Explicitly anchor the project root so Turbopack never walks up past web/
    // and accidentally picks up /Users/<user>/package.json or the repo root.
    root: path.resolve(__dirname),
  },
  async rewrites() {
    return [
      // Proxy auth flow and all API calls to the Go backend
      {
        source: "/auth/:path*",
        destination: `${backendURL}/auth/:path*`,
      },
      {
        source: "/api/:path*",
        destination: `${backendURL}/api/:path*`,
      },
    ];
  },
};

export default nextConfig;
