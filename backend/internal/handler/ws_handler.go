package handler

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/diyorend/syncroom/internal/domain"
	"github.com/diyorend/syncroom/internal/middleware"
	"github.com/diyorend/syncroom/internal/repository"
	"github.com/diyorend/syncroom/internal/room"
	"github.com/diyorend/syncroom/internal/service"
	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

type WSHandler struct {
	manager  *room.Manager
	roomSvc  *service.RoomService
	chatRepo repository.ChatStore
}

func NewWSHandler(manager *room.Manager, roomSvc *service.RoomService, chatRepo repository.ChatStore) *WSHandler {
	return &WSHandler{manager: manager, roomSvc: roomSvc, chatRepo: chatRepo}
}

func (h *WSHandler) Connect(c echo.Context) error {
	code := c.Param("code")

	name := c.QueryParam("name")
	if claims := middleware.GetClaims(c); claims != nil {
		name = claims.Email
	}
	if name == "" {
		name = "Guest"
	}

	dbRoom, err := h.roomSvc.GetByCode(c.Request().Context(), code)
	if err != nil {
		if errors.Is(err, domain.ErrRoomNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "room not found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to find room"})
	}

	conn, err := upgrader.Upgrade(c.Response(), c.Request(), nil)
	if err != nil {
		slog.Error("ws upgrade failed", "room", code, "err", err)
		return nil
	}

	r := h.manager.GetOrCreate(c.Request().Context(), code, dbRoom.ID, dbRoom.VideoURL)

	clientID := generateClientID()

	h.manager.Join(r, clientID, name, conn)
	defer h.manager.Leave(r, clientID)

	slog.Info("ws connected", "room", code, "client", clientID, "name", name)

	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			slog.Info("ws disconnected", "room", code, "client", clientID)
			break
		}

		var ev domain.ClientEvent
		if err := json.Unmarshal(msg, &ev); err != nil {
			slog.Warn("ws bad message", "room", code, "client", clientID, "err", err)
			continue
		}

		ev.ClientID = clientID

		if ev.Type == domain.EventChat && ev.ChatBody != "" {
			go func(body string) {
				ctx := c.Request().Context()
				if _, saveErr := h.chatRepo.Save(ctx, dbRoom.ID, name, body); saveErr != nil {
					slog.Error("failed to persist chat", "room", code, "err", saveErr)
				}
			}(ev.ChatBody)
		}

		if ev.Type == domain.EventSetVideo && ev.VideoURL != "" {
			go func(url string) {
				ctx := c.Request().Context()
				if updateErr := h.roomSvc.UpdateVideoURL(ctx, dbRoom.ID, url); updateErr != nil {
					slog.Error("failed to persist video url", "room", code, "err", updateErr)
				}
			}(ev.VideoURL)
		}

		h.manager.Dispatch(r, clientID, ev)
	}

	return nil
}

func generateClientID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "fallback-id"
	}
	return fmt.Sprintf("%x", b)
}
