CREATE TABLE workspace (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  name          text NOT NULL,
  slug          text NOT NULL,
  work_item_counter bigint NOT NULL DEFAULT 0,
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX uq_workspace_slug ON workspace (slug);
COMMENT ON INDEX uq_workspace_slug IS 'slug globally unique; reserved-word validation in the service layer (identity domain M1)';
