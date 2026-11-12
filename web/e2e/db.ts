// Direct database setup for browser tests that need state no API can create (T034: an open
// discrepancy). Uses the compose Postgres, so it only works against the local/CI stack.
import { execFileSync } from 'node:child_process';
import { randomUUID } from 'node:crypto';
import { fileURLToPath } from 'node:url';

const repoRoot = fileURLToPath(new URL('../..', import.meta.url));

/** True when the tests run against the local compose stack (not WEB_BASE_URL) and its Postgres is reachable. */
export const canSeedDatabase = !process.env.WEB_BASE_URL && postgresRunning();

function postgresRunning(): boolean {
  try {
    const out = execFileSync('docker', ['compose', 'ps', '--status', 'running', '--services'], { cwd: repoRoot, encoding: 'utf8', stdio: ['ignore', 'pipe', 'ignore'] });
    return out.split('\n').includes('postgres');
  } catch {
    return false;
  }
}

function psql(sql: string): void {
  execFileSync('docker', ['compose', 'exec', '-T', 'postgres', 'psql', '-U', 'wallet', '-d', 'wallet', '-v', 'ON_ERROR_STOP=1', '-q', '-c', sql], {
    cwd: repoRoot,
    stdio: ['ignore', 'pipe', 'pipe'],
  });
}

/** Inserts an OPEN WARNING discrepancy (no transaction) and returns its ID and marker. */
export function seedDiscrepancy(): { id: string; marker: string } {
  const id = randomUUID();
  const marker = `e2e-${id.slice(0, 8)}`;
  const details = JSON.stringify({ marker, reason: 'seeded by e2e/ops.spec.ts', html: '<b>not bold</b>' });
  psql(
    `INSERT INTO reconciliation_discrepancies (id, kind, severity, transaction_id, details, status, created_at)
     VALUES ('${id}', 'UNMATCHED_BANK_MOVEMENT', 'WARNING', NULL, '${details.replaceAll("'", "''")}'::jsonb, 'OPEN', now())`,
  );
  return { id, marker };
}

/** Removes a seeded discrepancy so a failed run doesn't leave the shared stack in "warning". */
export function deleteDiscrepancy(id: string): void {
  psql(`DELETE FROM reconciliation_discrepancies WHERE id = '${id}'`);
}
