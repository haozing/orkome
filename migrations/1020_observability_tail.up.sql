CREATE TABLE IF NOT EXISTS search_index_change (
    entity_type  TEXT        NOT NULL CHECK (entity_type IN ('work_item', 'comment', 'project')),
    entity_id    UUID        NOT NULL,
    workspace_id UUID        NOT NULL REFERENCES workspace(id),
    change_xid   XID8        NOT NULL,
    changed_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (entity_type, entity_id)
);

CREATE INDEX IF NOT EXISTS idx_search_index_change_workspace_xid
    ON search_index_change (workspace_id, change_xid, entity_type, entity_id);

CREATE INDEX IF NOT EXISTS idx_search_index_change_changed_at
    ON search_index_change (changed_at);

CREATE TABLE IF NOT EXISTS search_index_prune_mark (
    singleton          BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    pruned_through_xid XID8    NOT NULL
);

CREATE OR REPLACE FUNCTION record_search_index_change()
RETURNS TRIGGER LANGUAGE plpgsql AS $function$
DECLARE
    changed_id UUID;
    changed_workspace_id UUID;
BEGIN
    IF TG_OP = 'DELETE' THEN
        changed_id := OLD.id;
        changed_workspace_id := OLD.workspace_id;
    ELSE
        changed_id := NEW.id;
        changed_workspace_id := NEW.workspace_id;
    END IF;

    INSERT INTO search_index_change (entity_type, entity_id, workspace_id, change_xid, changed_at)
    VALUES (TG_ARGV[0], changed_id, changed_workspace_id, pg_current_xact_id(), now())
    ON CONFLICT (entity_type, entity_id) DO UPDATE
        SET workspace_id = EXCLUDED.workspace_id,
            change_xid = EXCLUDED.change_xid,
            changed_at = EXCLUDED.changed_at;
    RETURN NULL;
END
$function$;

DROP TRIGGER IF EXISTS trg_work_item_search_index_change ON work_item;
CREATE TRIGGER trg_work_item_search_index_change
AFTER INSERT OR UPDATE OR DELETE ON work_item
FOR EACH ROW
WHEN (current_setting('orkome.workspace_teardown', true) IS DISTINCT FROM 'on')
EXECUTE FUNCTION record_search_index_change('work_item');

DROP TRIGGER IF EXISTS trg_comment_search_index_change ON comment;
CREATE TRIGGER trg_comment_search_index_change
AFTER INSERT OR DELETE ON comment
FOR EACH ROW
WHEN (current_setting('orkome.workspace_teardown', true) IS DISTINCT FROM 'on')
EXECUTE FUNCTION record_search_index_change('comment');

DROP TRIGGER IF EXISTS trg_comment_update_search_index_change ON comment;
CREATE TRIGGER trg_comment_update_search_index_change
AFTER UPDATE OF content, deleted_at, work_item_id, created_at ON comment
FOR EACH ROW
WHEN (current_setting('orkome.workspace_teardown', true) IS DISTINCT FROM 'on')
EXECUTE FUNCTION record_search_index_change('comment');

DROP TRIGGER IF EXISTS trg_project_search_index_change ON project;
CREATE TRIGGER trg_project_search_index_change
AFTER INSERT OR UPDATE OR DELETE ON project
FOR EACH ROW
WHEN (current_setting('orkome.workspace_teardown', true) IS DISTINCT FROM 'on')
EXECUTE FUNCTION record_search_index_change('project');

CREATE TABLE IF NOT EXISTS client_usage_daily (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id    UUID NOT NULL REFERENCES workspace(id),
    user_id         UUID NOT NULL REFERENCES "user"(id),
    client_type     TEXT NOT NULL CHECK (client_type IN ('web', 'desktop')),
    install_id      UUID NOT NULL,
    activity_date   DATE NOT NULL,
    client_version  TEXT NOT NULL,
    os              TEXT NOT NULL,
    first_active_at TIMESTAMPTZ NOT NULL,
    last_active_at  TIMESTAMPTZ NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (first_active_at <= last_active_at),
    UNIQUE (user_id, client_type, install_id, activity_date)
);

CREATE INDEX IF NOT EXISTS idx_client_usage_daily_activity
    ON client_usage_daily (activity_date, client_type, user_id);
CREATE INDEX IF NOT EXISTS idx_client_usage_daily_workspace
    ON client_usage_daily (workspace_id);

CREATE TABLE IF NOT EXISTS autopilot_rule_version (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id     UUID NOT NULL REFERENCES workspace(id),
    autopilot_id     UUID NOT NULL REFERENCES autopilot(id) ON DELETE CASCADE,
    published_by_type TEXT NOT NULL DEFAULT 'member',
    published_by_id  UUID,
    config_summary   JSONB NOT NULL DEFAULT '{}',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_autopilot_rule_version_active
    ON autopilot_rule_version (workspace_id, autopilot_id, created_at DESC);

CREATE TABLE IF NOT EXISTS autopilot_collaborator (
    autopilot_id UUID NOT NULL REFERENCES autopilot(id) ON DELETE CASCADE,
    workspace_id UUID NOT NULL REFERENCES workspace(id),
    user_type    TEXT NOT NULL CHECK (user_type IN ('member')),
    user_id      UUID NOT NULL REFERENCES "user"(id),
    granted_by   UUID NOT NULL REFERENCES "user"(id),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (autopilot_id, user_type, user_id)
);

CREATE INDEX IF NOT EXISTS idx_autopilot_collaborator_user
    ON autopilot_collaborator (user_type, user_id);

CREATE TABLE IF NOT EXISTS agent_to_label (
    agent_id   UUID NOT NULL REFERENCES agent(id),
    label_id   UUID NOT NULL REFERENCES label(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (agent_id, label_id)
);

CREATE TABLE IF NOT EXISTS skill_to_label (
    skill_id   UUID NOT NULL REFERENCES skill(id),
    label_id   UUID NOT NULL REFERENCES label(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (skill_id, label_id)
);
