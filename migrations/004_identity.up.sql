CREATE TABLE verification_code (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  email       text NOT NULL,
  code_hash   text NOT NULL,
  expires_at  timestamptz NOT NULL,
  attempts    int NOT NULL DEFAULT 0,
  consumed_at timestamptz,
  created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_vcode_email_pending ON verification_code (email, created_at DESC)
  WHERE consumed_at IS NULL;

CREATE TABLE workspace_invitation (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspace(id),
  email        text NOT NULL,
  role         text NOT NULL CHECK (role IN ('admin','member')),
  status       text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','accepted','declined','revoked')),
  expires_at   timestamptz NOT NULL,
  created_by   uuid NOT NULL REFERENCES "user"(id),
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_invitation_email ON workspace_invitation (email) WHERE status = 'pending';

CREATE TABLE workspace_share_link (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspace(id),
  code         text NOT NULL,
  role         text NOT NULL CHECK (role IN ('admin','member')),
  max_uses     int NOT NULL,
  uses         int NOT NULL DEFAULT 0,
  expires_at   timestamptz NOT NULL,
  created_by   uuid NOT NULL REFERENCES "user"(id),
  created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX uq_share_link_code ON workspace_share_link (code);

CREATE TABLE personal_access_token (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id      uuid NOT NULL REFERENCES "user"(id),
  name         text NOT NULL,
  token_hash   text NOT NULL,
  prefix       text NOT NULL,
  last_used_at timestamptz,
  expires_at   timestamptz,
  revoked_at   timestamptz,
  created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_pat_user ON personal_access_token (user_id) WHERE revoked_at IS NULL;
CREATE UNIQUE INDEX uq_pat_hash ON personal_access_token (token_hash);

CREATE TABLE pinned_item (
  id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id    uuid NOT NULL REFERENCES "user"(id),
  kind       text NOT NULL,
  ref_id     uuid NOT NULL,
  position   int NOT NULL DEFAULT 0,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (user_id, kind, ref_id)
);
COMMENT ON TABLE pinned_item IS 'sidebar pins (endpoints ship with M2+; table in place first)';
