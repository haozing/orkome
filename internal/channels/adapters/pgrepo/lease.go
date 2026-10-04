package pgrepo

import (
	"context"
	"time"

	"orkome/internal/channels/app"
)

type SQLLeaseStore struct{ Repo *Repo }

func NewSQLLeaseStore(r *Repo) *SQLLeaseStore { return &SQLLeaseStore{Repo: r} }

var _ app.LeaseStore = (*SQLLeaseStore)(nil)

func (s *SQLLeaseStore) TryAcquire(ctx context.Context, installationID, token string, ttl time.Duration) error {
	tag, err := s.Repo.q(ctx).Exec(ctx, `
UPDATE channel_installation
SET lease_owner=$2, lease_expires_at=now() + make_interval(secs => $3::int), updated_at=now()
WHERE id=$1
  AND (lease_owner IS NULL OR lease_owner=$2
       OR (lease_expires_at IS NOT NULL AND lease_expires_at < now()))`,
		installationID, token, int(ttl.Seconds()))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return app.ErrLeaseNotAcquired
	}
	return nil
}

func (s *SQLLeaseStore) Renew(ctx context.Context, installationID, token string, ttl time.Duration) error {
	tag, err := s.Repo.q(ctx).Exec(ctx, `
UPDATE channel_installation
SET lease_expires_at=now() + make_interval(secs => $3::int), updated_at=now()
WHERE id=$1 AND lease_owner=$2`,
		installationID, token, int(ttl.Seconds()))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return app.ErrLeaseNotAcquired
	}
	return nil
}

func (s *SQLLeaseStore) Release(ctx context.Context, installationID, token string) error {
	_, err := s.Repo.q(ctx).Exec(ctx, `
UPDATE channel_installation SET lease_owner=NULL, lease_expires_at=NULL, updated_at=now()
WHERE id=$1 AND lease_owner=$2`, installationID, token)
	return err
}

func (s *SQLLeaseStore) ListHeld(ctx context.Context, ids []string) (map[string]struct{}, error) {
	out := map[string]struct{}{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.Repo.q(ctx).Query(ctx, `
SELECT id::text FROM channel_installation
WHERE id = ANY($1) AND lease_owner IS NOT NULL AND lease_expires_at > now()`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = struct{}{}
	}
	return out, rows.Err()
}
