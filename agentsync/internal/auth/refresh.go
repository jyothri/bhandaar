package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Refresh-token errors.
var (
	ErrInvalidRefresh = errors.New("refresh token invalid, expired or revoked")
	ErrRefreshReused  = errors.New("refresh token reused; its family is revoked")
	ErrAgentMismatch  = errors.New("refresh token belongs to another agent")
)

// RefreshGrace is how long after a rotation the old token is accepted once
// more, for an agent that lost the refresh response.
const RefreshGrace = 30 * time.Second

const refreshPrefix = "rt_"

// Tokens issues and rotates token pairs.
type Tokens struct {
	Pool       *pgxpool.Pool
	Secret     []byte
	AccessTTL  time.Duration
	RefreshTTL time.Duration
	// Now is the clock; nil means time.Now. Token timestamps come from it, not
	// from Postgres, so tests can move time.
	Now func() time.Time
}

// Pair is a newly issued access and refresh token.
type Pair struct {
	AccessToken  string
	AccessTTL    time.Duration
	RefreshToken string
	RefreshTTL   time.Duration
	Username     string
}

func (t *Tokens) now() time.Time {
	if t.Now != nil {
		return t.Now().UTC()
	}
	return time.Now().UTC()
}

func hashRefresh(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// Login issues a pair that starts a new refresh-token family.
func (t *Tokens) Login(ctx context.Context, userID int64, username, agentID string) (Pair, error) {
	var p Pair
	err := pgx.BeginFunc(ctx, t.Pool, func(tx pgx.Tx) error {
		var err error
		p, err = t.issue(ctx, tx, uuid.New(), nil, userID, agentID, username)
		return err
	})
	return p, err
}

// issue creates an access token and a refresh token in family, replacing
// parent (if any).
func (t *Tokens) issue(ctx context.Context, tx pgx.Tx, family uuid.UUID, parent *int64,
	userID int64, agentID, username string) (Pair, error) {
	b := make([]byte, 32) // 256 bits
	if _, err := rand.Read(b); err != nil {
		return Pair{}, err
	}
	refresh := refreshPrefix + base64.RawURLEncoding.EncodeToString(b)
	now := t.now()
	if _, err := tx.Exec(ctx, `
		INSERT INTO agent_refresh_tokens (token_hash, family_id, parent_id, user_id, agent_id, issued_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		hashRefresh(refresh), family, parent, userID, agentID, now, now.Add(t.RefreshTTL)); err != nil {
		return Pair{}, err
	}
	access, err := IssueAccessToken(t.Secret, Principal{UserID: userID, AgentID: agentID}, now, t.AccessTTL)
	if err != nil {
		return Pair{}, err
	}
	return Pair{AccessToken: access, AccessTTL: t.AccessTTL, RefreshToken: refresh, RefreshTTL: t.RefreshTTL, Username: username}, nil
}

// Refresh exchanges a refresh token for a new pair. agentID, if not empty,
// must be the agent the token was issued to; version, if not empty, is
// recorded as the agent's last version.
//
// The token's row is locked for the exchange, so concurrent refreshes with
// one token are handled one after the other:
//   - a current token is rotated: marked rotated, and a successor issued;
//   - a token rotated within RefreshGrace, whose successor hasn't been used,
//     is accepted once more: the unused successor is revoked and a fresh pair
//     issued (the agent lost the previous response);
//   - any other rotated token is a reuse, and so is the successor a grace
//     replay revoked: the whole family is revoked and ErrRefreshReused
//     returned. The second rule keeps theft detectable: if a thief replays a
//     stolen token within the window, the owner's next refresh presents the
//     grace-revoked successor, which revokes the thief's pair too.
func (t *Tokens) Refresh(ctx context.Context, token, agentID, version string) (Pair, error) {
	if !strings.HasPrefix(token, refreshPrefix) {
		return Pair{}, ErrInvalidRefresh
	}
	var (
		pair   Pair
		result error // an error to return after committing
	)
	err := pgx.BeginFunc(ctx, t.Pool, func(tx pgx.Tx) error {
		var (
			id                        int64
			family                    uuid.UUID
			userID                    int64
			tokenAgent, username      string
			expiresAt                 time.Time
			rotatedAt, graceAt, revAt *time.Time
			revReason                 *string
			userDisabled              *time.Time
		)
		err := tx.QueryRow(ctx, `
			SELECT t.id, t.family_id, t.user_id, t.agent_id::text, t.expires_at, t.rotated_at, t.grace_used_at,
			       t.revoked_at, t.revoked_reason, u.username, u.disabled_at
			  FROM agent_refresh_tokens t JOIN agent_users u ON u.id = t.user_id
			 WHERE t.token_hash = $1
			   FOR UPDATE OF t`, hashRefresh(token)).
			Scan(&id, &family, &userID, &tokenAgent, &expiresAt, &rotatedAt, &graceAt, &revAt, &revReason, &username, &userDisabled)
		if errors.Is(err, pgx.ErrNoRows) {
			result = ErrInvalidRefresh
			return nil
		}
		if err != nil {
			return err
		}
		now := t.now()
		revokeFamily := func() error {
			_, err := tx.Exec(ctx, `
				UPDATE agent_refresh_tokens SET revoked_at = $2, revoked_reason = 'reuse'
				 WHERE family_id = $1 AND revoked_at IS NULL`, family, now)
			result = ErrRefreshReused
			return err
		}
		switch {
		case agentID != "" && !strings.EqualFold(agentID, tokenAgent):
			result = ErrAgentMismatch
			return nil
		case revAt != nil && revReason != nil && *revReason == "grace":
			return revokeFamily()
		case revAt != nil || userDisabled != nil || !now.Before(expiresAt):
			result = ErrInvalidRefresh
			return nil
		}

		if rotatedAt != nil {
			// A replay. Accept it once within the grace window, as long as
			// the successor was never used (so the agent never received it).
			var successorUsed bool
			if err := tx.QueryRow(ctx, `
				SELECT EXISTS (SELECT 1 FROM agent_refresh_tokens WHERE parent_id = $1 AND rotated_at IS NOT NULL)`,
				id).Scan(&successorUsed); err != nil {
				return err
			}
			if graceAt != nil || successorUsed || now.Sub(*rotatedAt) > RefreshGrace {
				return revokeFamily()
			}
			if _, err := tx.Exec(ctx, `
				UPDATE agent_refresh_tokens SET revoked_at = $2, revoked_reason = 'grace'
				 WHERE parent_id = $1 AND revoked_at IS NULL`,
				id, now); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx,
				"UPDATE agent_refresh_tokens SET grace_used_at = $2 WHERE id = $1", id, now); err != nil {
				return err
			}
		} else if _, err := tx.Exec(ctx,
			"UPDATE agent_refresh_tokens SET rotated_at = $2 WHERE id = $1", id, now); err != nil {
			return err
		}

		if _, err := tx.Exec(ctx, `
			UPDATE agent_agents SET last_seen_at = $2, last_version = coalesce(nullif($3, ''), last_version)
			 WHERE id = $1`, tokenAgent, now, version); err != nil {
			return err
		}
		pair, err = t.issue(ctx, tx, family, &id, userID, tokenAgent, username)
		return err
	})
	if err != nil {
		return Pair{}, err
	}
	if result != nil {
		return Pair{}, result
	}
	return pair, nil
}

// Logout revokes the token's whole family. An unknown token is not an error.
func (t *Tokens) Logout(ctx context.Context, token string) error {
	_, err := t.Pool.Exec(ctx, `
		UPDATE agent_refresh_tokens SET revoked_at = $2, revoked_reason = 'logout'
		 WHERE family_id = (SELECT family_id FROM agent_refresh_tokens WHERE token_hash = $1)
		   AND revoked_at IS NULL`, hashRefresh(token), t.now())
	return err
}
