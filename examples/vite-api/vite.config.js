// Host-run frontend + API in one dev server (no separate API process to
// keep this example minimal): a Vite plugin adds one middleware route
// that talks to the Postgres stackfork isolated, same visits-counter
// pattern as examples/node-postgres, so two worktrees of this example
// (one containerized, one host-run) prove the same isolation two ways.
import { defineConfig } from "vite";
import pg from "pg";

function apiPlugin() {
  let pool;
  return {
    name: "vite-api-example",
    configureServer(server) {
      pool = new pg.Pool({ connectionString: process.env.DATABASE_URL });
      pool
        .query(
          "CREATE TABLE IF NOT EXISTS visits (id SERIAL PRIMARY KEY, visited_at TIMESTAMPTZ NOT NULL DEFAULT now())"
        )
        .catch((err) => console.error("migration failed:", err));

      server.middlewares.use("/api/visits", async (req, res) => {
        try {
          await pool.query("INSERT INTO visits DEFAULT VALUES");
          const { rows } = await pool.query("SELECT count(*) AS total FROM visits");
          res.setHeader("Content-Type", "application/json");
          res.end(JSON.stringify({ visits: Number(rows[0].total) }));
        } catch (err) {
          res.statusCode = 500;
          res.end(String(err));
        }
      });
    },
  };
}

export default defineConfig({
  plugins: [apiPlugin()],
  server: {
    host: "0.0.0.0",
    // The proxy's hostname (registered via `stackfork route add`) isn't
    // localhost/127.0.0.1, so Vite's dev-server host check needs it
    // allowed explicitly.
    allowedHosts: true,
  },
});
