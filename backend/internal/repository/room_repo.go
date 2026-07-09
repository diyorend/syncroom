package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/diyorend/syncroom/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type RoomRepo struct {
	db *pgxpool.Pool
}

func NewRoomRepo(db *pgxpool.Pool) *RoomRepo {
	return &RoomRepo{db: db}
}

var _ RoomStore = (*RoomRepo)(nil)

func (r *RoomRepo) Create(ctx context.Context, creatorID, code string) (*domain.Room, error) {
	var room domain.Room
	err := r.db.QueryRow(ctx,
		`INSERT INTO rooms (creator_id, code)
		 VALUES ($1, $2)
		 RETURNING id, code, creator_id, COALESCE(video_url, ''), created_at`,
		creatorID, code,
	).Scan(&room.ID, &room.Code, &room.CreatorID, &room.VideoURL, &room.CreatedAt)

	if err != nil {
		if strings.Contains(err.Error(), "23505") {
			return nil, fmt.Errorf("room code %s: %w", code, domain.ErrAlreadyExists)
		}
		return nil, fmt.Errorf("RoomRepo.Create: %w", err)
	}
	return &room, nil
}

func (r *RoomRepo) GetByCode(ctx context.Context, code string) (*domain.Room, error) {
	var room domain.Room
	err := r.db.QueryRow(ctx,
		`SELECT id, code, creator_id, COALESCE(video_url, ''), created_at
		 FROM rooms WHERE code = $1`,
		code,
	).Scan(&room.ID, &room.Code, &room.CreatorID, &room.VideoURL, &room.CreatedAt)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("room %s: %w", code, domain.ErrRoomNotFound)
		}
		return nil, fmt.Errorf("RoomRepo.GetByCode: %w", err)
	}
	return &room, nil
}

func (r *RoomRepo) UpdateVideoURL(ctx context.Context, roomID, videoURL string) error {
	_, err := r.db.Exec(ctx,
		`UPDATE rooms SET video_url = $1 WHERE id = $2`,
		videoURL, roomID,
	)
	if err != nil {
		return fmt.Errorf("RoomRepo.UpdateVideoURL: %w", err)
	}
	return nil
}
