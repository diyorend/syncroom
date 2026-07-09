package handler

import (
	"errors"
	"net/http"

	"github.com/diyorend/syncroom/internal/domain"
	"github.com/diyorend/syncroom/internal/middleware"
	"github.com/diyorend/syncroom/internal/repository"
	"github.com/diyorend/syncroom/internal/service"
	"github.com/labstack/echo/v4"
)

type RoomHandler struct {
	roomSvc  *service.RoomService
	chatRepo repository.ChatStore
}

func NewRoomHandler(roomSvc *service.RoomService, chatRepo repository.ChatStore) *RoomHandler {
	return &RoomHandler{roomSvc: roomSvc, chatRepo: chatRepo}
}

func (h *RoomHandler) Create(c echo.Context) error {
	userID, err := middleware.RequireAuth(c)
	if err != nil {
		return err
	}
	room, err := h.roomSvc.Create(c.Request().Context(), userID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to create room"})
	}
	return c.JSON(http.StatusCreated, room)
}

func (h *RoomHandler) Get(c echo.Context) error {
	code := c.Param("code")
	room, err := h.roomSvc.GetByCode(c.Request().Context(), code)
	if err != nil {
		if errors.Is(err, domain.ErrRoomNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "room not found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to get room"})
	}

	// Fetch recent chat history so the page can show it on load without
	// waiting for subsequent WebSocket messages.
	msgs, err := h.chatRepo.ListByRoom(c.Request().Context(), room.ID, 50)
	if err != nil {
		msgs = []*domain.ChatMessage{}
	}
	if msgs == nil {
		msgs = []*domain.ChatMessage{}
	}

	return c.JSON(http.StatusOK, map[string]any{
		"room":     room,
		"messages": msgs,
	})
}
