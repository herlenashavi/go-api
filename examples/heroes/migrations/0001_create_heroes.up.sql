CREATE TABLE heroes (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name               TEXT NOT NULL,
    super_power        TEXT NOT NULL,
    status             TEXT NOT NULL DEFAULT 'active'
                           CHECK (status IN ('active', 'inactive', 'retired')),
    -- No foreign key to villains: the two services share no database,
    -- see Hero.NemesisVillainID's doc comment in hero.go.
    nemesis_villain_id TEXT,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    modified_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
