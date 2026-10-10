CREATE TABLE villains (
    id              TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
    name            TEXT NOT NULL,
    evil_power      TEXT NOT NULL,
    threat_level    TEXT NOT NULL DEFAULT 'medium'
                        CHECK (threat_level IN ('low', 'medium', 'high', 'critical')),
    status          TEXT NOT NULL DEFAULT 'active'
                        CHECK (status IN ('active', 'captured', 'defeated')),
    -- No foreign key to heroes: the two services share no database, see
    -- examples/heroes/hero.go's Hero.NemesisVillainID doc comment for
    -- the same point made from the other side.
    nemesis_hero_id TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    modified_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
