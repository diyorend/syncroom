package repository

import (
	"context"
	"fmt"

	"github.com/diyorend/syncroom/internal/domain"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ChatRepo struct {
	db *pgxpool.Pool
}

func NewChatRepo(db *pgxpool.Pool) *ChatRepo {
	return &ChatRepo{db: db}
}

var _ ChatStore = (*ChatRepo)(nil)

func (r *ChatRepo) Save(ctx context.Context, roomID, senderName, body string) (*domain.ChatMessage, error) {
	var m domain.ChatMessage
	err := r.db.QueryRow(ctx,
		`INSERT INTO chat_messages (room_id, sender_name, body)
		 VALUES ($1, $2, $3)
		 RETURNING id, room_id, sender_name, body, created_at`,
		roomID, senderName, body,
	).Scan(&m.ID, &m.RoomID, &m.SenderName, &m.Body, &m.CreatedAt)

	if err != nil {
		return nil, fmt.Errorf("ChatRepo.Save: %w", err)
	}
	return &m, nil
}

func (r *ChatRepo) ListByRoom(ctx context.Context, roomID string, limit int) ([]*domain.ChatMessage, error) {
	rows, err := r.db.Query(ctx,
		`SELECT id, room_id, sender_name, body, created_at
		 FROM chat_messages
		 WHERE room_id = $1
		 ORDER BY created_at ASC
		 LIMIT $2`,
		roomID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("ChatRepo.ListByRoom: %w", err)
	}
	defer rows.Close()

	var msgs []*domain.ChatMessage
	for rows.Next() {
		var m domain.ChatMessage
		if err := rows.Scan(&m.ID, &m.RoomID, &m.SenderName, &m.Body, &m.CreatedAt); err != nil {
			return nil, err
		}
		msgs = append(msgs, &m)
	}
	return msgs, rows.Err()
}
