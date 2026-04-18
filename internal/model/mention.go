package model

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// MentionStore persists the @username -> post linkage. The table
// itself is append-only on create; edits call SyncMentions which
// wipes and re-records so dropped mentions stop being reachable
// via ForUser.
type MentionStore struct {
	DB    *pgxpool.Pool
	Users *UserStore
}

// mentionPattern matches `@username` tokens. The username rules
// mirror isValidUsername: 3-32 chars of [a-zA-Z0-9_]. The pattern
// requires the `@` to be either at string start or preceded by
// whitespace / a non-word non-@ character, which keeps email
// addresses (foo@bar.com), double-@ spam (@@@handle), and HTML
// attribute boundaries from producing false positives.
var mentionPattern = regexp.MustCompile(`(?:^|[\s>(\[{,.;!?])@([a-zA-Z0-9_]{3,32})\b`)

// ExtractUsernames pulls unique lowercased usernames from the raw
// post content. The input can be plain text, Markdown, or Quill
// HTML - the regex works on all three because it anchors on the
// leading `@` rather than the surrounding grammar.
//
// Returned slice is stable in discovery order so tests can assert
// on it without sort(). Duplicates (case-insensitive) are dropped
// the second time they appear.
func ExtractUsernames(content string) []string {
	matches := mentionPattern.FindAllStringSubmatch(content, -1)
	seen := make(map[string]bool, len(matches))
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		lower := strings.ToLower(m[1])
		if seen[lower] {
			continue
		}
		seen[lower] = true
		out = append(out, lower)
	}
	return out
}

// RecordMentions resolves every username to an active user id and
// inserts rows into the mentions table. Duplicate (post, user)
// pairs are swallowed via ON CONFLICT DO NOTHING - safe to call
// multiple times for the same post with overlapping inputs. The
// return slice contains only the user ids that were actually
// resolved and recorded, so the caller can fan out notifications
// without having to filter separately.
func (s *MentionStore) RecordMentions(ctx context.Context, postID int64, usernames []string) ([]int64, error) {
	if len(usernames) == 0 || s.Users == nil {
		return nil, nil
	}
	out := make([]int64, 0, len(usernames))
	for _, name := range usernames {
		u, err := s.Users.FindByUsername(ctx, name)
		if err != nil {
			return out, fmt.Errorf("resolve mention %q: %w", name, err)
		}
		if u == nil || u.Status != UserStatusActive {
			// Silently skip unknown or non-active mentions. The raw
			// text stays in the post content; just no link, no
			// notification.
			continue
		}
		if _, err := s.DB.Exec(ctx,
			`INSERT INTO mentions (post_id, mentioned_user)
			 VALUES ($1, $2)
			 ON CONFLICT (post_id, mentioned_user) DO NOTHING`,
			postID, u.ID,
		); err != nil {
			return out, fmt.Errorf("insert mention: %w", err)
		}
		out = append(out, u.ID)
	}
	return out, nil
}

// SyncMentions replaces the set of mentions for a post. Used on
// post edit: mentions removed from the content disappear from
// the join table, new ones are inserted. Existing notifications
// are NOT retracted - read/unread state from the original notify
// stays as it was. Returns the user ids that were newly added
// by this sync (so the caller can decide whether to re-notify;
// current policy is "no, don't re-notify on edit").
func (s *MentionStore) SyncMentions(ctx context.Context, postID int64, usernames []string) ([]int64, error) {
	// Snapshot the existing set for diff.
	existing, err := s.DB.Query(ctx,
		`SELECT mentioned_user FROM mentions WHERE post_id = $1`, postID)
	if err != nil {
		return nil, fmt.Errorf("load existing mentions: %w", err)
	}
	have := map[int64]bool{}
	for existing.Next() {
		var uid int64
		if err := existing.Scan(&uid); err != nil {
			existing.Close()
			return nil, fmt.Errorf("scan existing mention: %w", err)
		}
		have[uid] = true
	}
	existing.Close()

	// Resolve fresh set.
	want := map[int64]bool{}
	for _, name := range usernames {
		if s.Users == nil {
			break
		}
		u, err := s.Users.FindByUsername(ctx, name)
		if err != nil {
			return nil, fmt.Errorf("resolve mention %q: %w", name, err)
		}
		if u == nil || u.Status != UserStatusActive {
			continue
		}
		want[u.ID] = true
	}

	// Delete removed, insert added.
	var toAdd []int64
	for uid := range want {
		if !have[uid] {
			toAdd = append(toAdd, uid)
		}
	}
	var toDel []int64
	for uid := range have {
		if !want[uid] {
			toDel = append(toDel, uid)
		}
	}

	if len(toDel) > 0 {
		if _, err := s.DB.Exec(ctx,
			`DELETE FROM mentions WHERE post_id = $1 AND mentioned_user = ANY($2)`,
			postID, toDel,
		); err != nil {
			return toAdd, fmt.Errorf("delete stale mentions: %w", err)
		}
	}
	for _, uid := range toAdd {
		if _, err := s.DB.Exec(ctx,
			`INSERT INTO mentions (post_id, mentioned_user)
			 VALUES ($1, $2)
			 ON CONFLICT (post_id, mentioned_user) DO NOTHING`,
			postID, uid,
		); err != nil {
			return toAdd, fmt.Errorf("insert mention: %w", err)
		}
	}
	return toAdd, nil
}

// ForUser returns the post ids that mention the given user,
// newest first, bounded by limit. Index idx_mentions_user_created
// makes this a single index scan.
func (s *MentionStore) ForUser(ctx context.Context, userID int64, limit int) ([]int64, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.DB.Query(ctx,
		`SELECT post_id FROM mentions
		  WHERE mentioned_user = $1
		  ORDER BY created_at DESC
		  LIMIT $2`,
		userID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("mentions for user: %w", err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan mention: %w", err)
		}
		out = append(out, id)
	}
	return out, nil
}
