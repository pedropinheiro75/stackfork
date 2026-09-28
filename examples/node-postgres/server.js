// Minimal real app for the stackfork quick start: no framework, one
// dependency (pg). Every request records and counts a visit in Postgres,
// so two worktrees of this example running side by side via stackfork
// visibly keep separate counts - proof the isolation is real, not just
// "two servers happened to start".
import http from "node:http";
import pg from "pg";

const port = Number(process.env.PORT || 3000);
const pool = new pg.Pool({ connectionString: process.env.DATABASE_URL });

// One migration: idempotent, safe to run on every start. A real app would
// use a migration tool; this is deliberately as small as the example
// needs to prove the pattern.
async function migrate() {
  await pool.query(`
    CREATE TABLE IF NOT EXISTS visits (
      id SERIAL PRIMARY KEY,
      visited_at TIMESTAMPTZ NOT NULL DEFAULT now()
    )
  `);
}

// Postgres may still be finishing startup even with depends_on's
// service_healthy condition in slow CI environments; a short retry loop
// is cheaper than a flaky example.
async function migrateWithRetry(attempts = 10) {
  for (let i = 1; i <= attempts; i++) {
    try {
      await migrate();
      return;
    } catch (err) {
      if (i === attempts) throw err;
      await new Promise((r) => setTimeout(r, 500 * i));
    }
  }
}

const server = http.createServer(async (req, res) => {
  try {
    await pool.query("INSERT INTO visits DEFAULT VALUES");
    const { rows } = await pool.query("SELECT count(*) AS total FROM visits");
    res.writeHead(200, { "Content-Type": "application/json" });
    res.end(JSON.stringify({ visits: Number(rows[0].total) }));
  } catch (err) {
    res.writeHead(500, { "Content-Type": "text/plain" });
    res.end(String(err));
  }
});

migrateWithRetry()
  .then(() => {
    server.listen(port, "0.0.0.0", () => {
      console.log(`listening on :${port}`);
    });
  })
  .catch((err) => {
    console.error("migration failed:", err);
    process.exit(1);
  });
