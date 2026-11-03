-- Baseline: marks the start of the schema history. It changes nothing;
-- applying it creates schema_migrations so /readyz has a version to compare against.
SELECT 1;
