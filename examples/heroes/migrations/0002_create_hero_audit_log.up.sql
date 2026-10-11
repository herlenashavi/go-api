-- Backs PGHeroStore.CreateWithAudit (store_pg.go) — the dbx.WithTx demo
-- where a hero insert and its audit row must commit or roll back together.
CREATE TABLE hero_audit_log (
    id         BIGSERIAL PRIMARY KEY,
    hero_id    UUID NOT NULL REFERENCES heroes(id) ON DELETE CASCADE,
    actor      TEXT NOT NULL,
    event      TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX hero_audit_log_hero_id_idx ON hero_audit_log (hero_id);
