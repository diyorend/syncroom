package service

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"strings"

	"github.com/diyorend/syncroom/internal/domain"
	"github.com/diyorend/syncroom/internal/repository"
)

type RoomService struct {
	roomStore repository.RoomStore
}

func NewRoomService(roomStore repository.RoomStore) *RoomService {
	return &RoomService{roomStore: roomStore}
}

func (s *RoomService) Create(ctx context.Context, creatorID string) (*domain.Room, error) {
	code, err := generateCode()
	if err != nil {
		return nil, fmt.Errorf("RoomService.Create: generate code: %w", err)
	}
	room, err := s.roomStore.Create(ctx, creatorID, code)
	if err != nil {
		return nil, fmt.Errorf("RoomService.Create: %w", err)
	}
	return room, nil
}

func (s *RoomService) GetByCode(ctx context.Context, code string) (*domain.Room, error) {
	return s.roomStore.GetByCode(ctx, strings.ToUpper(code))
}

func (s *RoomService) UpdateVideoURL(ctx context.Context, roomID, videoURL string) error {
	return s.roomStore.UpdateVideoURL(ctx, roomID, videoURL)
}

var (
	adjectives = []string{"BLUE", "RED", "FAST", "CALM", "DARK", "BRIGHT", "WILD", "COOL", "KEEN", "BOLD"}
	nouns      = []string{"FOX", "OWL", "CAT", "WOLF", "BEAR", "HAWK", "LION", "LYNX", "CROW", "SWAN"}
)

func generateCode() (string, error) {
	adj, err := randomChoice(adjectives)
	if err != nil {
		return "", err
	}
	noun, err := randomChoice(nouns)
	if err != nil {
		return "", err
	}
	num, err := rand.Int(rand.Reader, big.NewInt(90))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s-%s-%d", adj, noun, num.Int64()+10), nil
}

func randomChoice(slice []string) (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(slice))))
	if err != nil {
		return "", err
	}
	return slice[n.Int64()], nil
}
