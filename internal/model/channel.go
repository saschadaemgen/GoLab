package model

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Channel struct {
	ID            int64     `json:"id"`
	Slug          string    `json:"slug"`
	Name          string    `json:"name"`
	Description   string    `json:"description"`
	ChannelType   string    `json:"channel_type"`
	CreatorID     int64     `json:"creator_id"`
	PowerRequired int       `json:"power_required"`
	MemberCount   int       `json:"member_count"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type ChannelStore struct {
	DB *pgxpool.Pool
}

func (s *ChannelStore) Create(ctx context.Context, slug, name, description, channelType string, creatorID int64) (*Channel, error) {
	ch := &Channel{}
	err := s.DB.QueryRow(ctx,
		`INSERT INTO channels (slug, name, description, channel_type, creator_id, member_count)
		 VALUES ($1, $2, $3, $4, $5, 1)
		 RETURNING id, slug, name, description, channel_type, creator_id, power_required,
		           member_count, created_at, updated_at`,
		slug, name, description, channelType, creatorID,
	).Scan(
		&ch.ID, &ch.Slug, &ch.Name, &ch.Description, &ch.ChannelType,
		&ch.CreatorID, &ch.PowerRequired, &ch.MemberCount, &ch.CreatedAt, &ch.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("creating channel: %w", err)
	}

	// Auto-join creator
	_, err = s.DB.Exec(ctx,
		`INSERT INTO channel_members (channel_id, user_id) VALUES ($1, $2)`,
		ch.ID, creatorID,
	)
	if err != nil {
		return nil, fmt.Errorf("joining creator to channel: %w", err)
	}

	return ch, nil
}

func (s *ChannelStore) FindBySlug(ctx context.Context, slug string) (*Channel, error) {
	ch := &Channel{}
	err := s.DB.QueryRow(ctx,
		`SELECT id, slug, name, description, channel_type, creator_id, power_required,
		        member_count, created_at, updated_at
		 FROM channels WHERE slug = $1`,
		slug,
	).Scan(
		&ch.ID, &ch.Slug, &ch.Name, &ch.Description, &ch.ChannelType,
		&ch.CreatorID, &ch.PowerRequired, &ch.MemberCount, &ch.CreatedAt, &ch.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("finding channel by slug: %w", err)
	}
	return ch, nil
}

func (s *ChannelStore) ListPublic(ctx context.Context, limit, offset int) ([]Channel, error) {
	rows, err := s.DB.Query(ctx,
		`SELECT id, slug, name, description, channel_type, creator_id, power_required,
		        member_count, created_at, updated_at
		 FROM channels WHERE channel_type = 'public'
		 ORDER BY member_count DESC, created_at DESC
		 LIMIT $1 OFFSET $2`,
		limit, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("listing channels: %w", err)
	}
	defer rows.Close()

	var channels []Channel
	for rows.Next() {
		var ch Channel
		if err := rows.Scan(
			&ch.ID, &ch.Slug, &ch.Name, &ch.Description, &ch.ChannelType,
			&ch.CreatorID, &ch.PowerRequired, &ch.MemberCount, &ch.CreatedAt, &ch.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning channel: %w", err)
		}
		channels = append(channels, ch)
	}
	return channels, nil
}

func (s *ChannelStore) IsMember(ctx context.Context, channelID, userID int64) (bool, error) {
	var exists bool
	err := s.DB.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM channel_members WHERE channel_id = $1 AND user_id = $2)`,
		channelID, userID,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("checking membership: %w", err)
	}
	return exists, nil
}

func (s *ChannelStore) Join(ctx context.Context, channelID, userID int64) error {
	_, err := s.DB.Exec(ctx,
		`INSERT INTO channel_members (channel_id, user_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		channelID, userID,
	)
	if err != nil {
		return fmt.Errorf("joining channel: %w", err)
	}

	_, err = s.DB.Exec(ctx,
		`UPDATE channels SET member_count = (SELECT COUNT(*) FROM channel_members WHERE channel_id = $1), updated_at = NOW() WHERE id = $1`,
		channelID,
	)
	if err != nil {
		return fmt.Errorf("updating member count: %w", err)
	}

	return nil
}

func (s *ChannelStore) Leave(ctx context.Context, channelID, userID int64) error {
	_, err := s.DB.Exec(ctx,
		`DELETE FROM channel_members WHERE channel_id = $1 AND user_id = $2`,
		channelID, userID,
	)
	if err != nil {
		return fmt.Errorf("leaving channel: %w", err)
	}

	_, err = s.DB.Exec(ctx,
		`UPDATE channels SET member_count = (SELECT COUNT(*) FROM channel_members WHERE channel_id = $1), updated_at = NOW() WHERE id = $1`,
		channelID,
	)
	if err != nil {
		return fmt.Errorf("updating member count: %w", err)
	}

	return nil
}
