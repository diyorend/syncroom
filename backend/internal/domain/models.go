package domain

import (
	"fmt"
	"time"
)

type User struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Password  string    `json:"-"`
	CreatedAt time.Time `json:"created_at"`
}

type Room struct {
	ID        string    `json:"id"`
	Code      string    `json:"code"`
	CreatorID string    `json:"creator_id"`
	VideoURL  string    `json:"video_url"`
	CreatedAt time.Time `json:"created_at"`
}

type ChatMessage struct {
	ID         string    `json:"id"`
	RoomID     string    `json:"room_id"`
	SenderName string    `json:"sender_name"`
	Body       string    `json:"body"`
	CreatedAt  time.Time `json:"created_at"`
}

type EventType string

const (
	EventPlay     EventType = "play"
	EventPause    EventType = "pause"
	EventSeek     EventType = "seek"
	EventChat     EventType = "chat"
	EventSync     EventType = "sync"
	EventPresence EventType = "presence"
	EventSetVideo EventType = "set_video"
)

type ClientEvent struct {
	Type            EventType `json:"type"`
	PositionSeconds float64   `json:"position_seconds,omitempty"`
	VideoURL        string    `json:"video_url,omitempty"`
	ChatBody        string    `json:"chat_body,omitempty"`
	ClientID        string    `json:"client_id,omitempty"`
}

type ServerEvent struct {
	Type            EventType `json:"type"`
	PositionSeconds float64   `json:"position_seconds,omitempty"`
	IsPlaying       bool      `json:"is_playing,omitempty"`
	VideoURL        string    `json:"video_url,omitempty"`
	LastUpdatedAt   time.Time `json:"last_updated_at,omitempty"`
	SenderName      string    `json:"sender_name,omitempty"`
	ChatBody        string    `json:"chat_body,omitempty"`
	OriginClientID  string    `json:"origin_client_id,omitempty"`
	YourClientID    string    `json:"your_client_id,omitempty"`
	Members         []string  `json:"members,omitempty"`
}

var (
	ErrNotFound      = fmt.Errorf("not found")
	ErrUnauthorized  = fmt.Errorf("unauthorized")
	ErrAlreadyExists = fmt.Errorf("already exists")
	ErrRoomNotFound  = fmt.Errorf("room not found")
)
