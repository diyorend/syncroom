package repository

import (
	"context"

	"github.com/diyorend/syncroom/internal/domain"
)

type UserStore interface {
	Create(ctx context.Context, email, hashedPassword string) (*domain.User, error)
	GetByEmail(ctx context.Context, email string) (*domain.User, error)
}

type RoomStore interface {
	Create(ctx context.Context, creatorID, code string) (*domain.Room, error)
	GetByCode(ctx context.Context, code string) (*domain.Room, error)
	UpdateVideoURL(ctx context.Context, roomID, videoURL string) error
}

type ChatStore interface {
	Save(ctx context.Context, roomID, senderName, body string) (*domain.ChatMessage, error)
	ListByRoom(ctx context.Context, roomID string, limit int) ([]*domain.ChatMessage, error)
}
