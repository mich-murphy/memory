package sessioncapture

import (
	"context"

	"github.com/mich-murphy/memory/internal/adapters"
)

// backupSQLiteAuthorized retains provenance on the original source, not the
// disposable prepared output. Metadata callbacks never grant body read authority.
func (q *Queue) backupSQLiteAuthorized(ctx context.Context, r Request, source *authorizedSource, dest string) error {
	return adapters.BackupSQLiteAuthorized(ctx, r.Path, dest, MaxSourceBytes, q.workingSpace, adapters.SQLiteAuthorizedOptions{
		ExpectedMain: source.before,
		Authorize: func(ctx context.Context, a adapters.SQLiteAuthorization) error {
			if !a.Present {
				// An absent sidecar contributes no body. Its absence is independently
				// verified before and after backup, without granting a sibling read route.
				return ctx.Err()
			}
			req := r
			req.Path = a.OriginalPath
			req.LogicalPath = "" // DB coordinates cannot claim a WAL sibling.
			ids := unionIdentities(source.Identities, a.Identities)
			// delivery=true is metadata-only: retain old restrictive evidence and check
			// every spelling without resampling authority or opening source bytes.
			_, err := q.check(ctx, evidence{Request: req, Identities: ids}, true)
			return err
		},
		ObserveInput: func(a adapters.SQLiteAuthorization) error {
			if !a.Present {
				return nil
			}
			ids := unionIdentities(source.Identities, a.Identities)
			if err := boundPolicyIdentities(ids); err != nil {
				return err
			}
			source.Identities = ids
			return nil
		},
	})
}
