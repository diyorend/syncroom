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
		// Production: restrict to your domain.
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

// Connect handles GET /ws/:code
// Query params:
//   - token=<jwt>   (optional; authenticated users get their email as display name)
//   - name=<string> (display name for guests; ignored if authenticated)
func (h *WSHandler) Connect(c echo.Context) error {
	code := c.Param("code")

	// Determine display name. Authenticated users get their email from JWT
	// claims; guests supply ?name=. Falls back to "Guest" if neither is set.
	name := c.QueryParam("name")
	if claims := middleware.GetClaims(c); claims != nil {
		name = claims.Email
	}
	if name == "" {
		name = "Guest"
	}

	// Look up the room record in Postgres to verify it exists and get its UUID.
	// GetOrCreate below uses this UUID as the in-memory Room's identifier.
	dbRoom, err := h.roomSvc.GetByCode(c.Request().Context(), code)
	if err != nil {
		if errors.Is(err, domain.ErrRoomNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "room not found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to find room"})
	}

	// Upgrade to WebSocket. After this point we cannot write a normal HTTP
	// response — the connection is a WebSocket. The upgrader handles the
	// 101 Switching Protocols response internally.
	conn, err := upgrader.Upgrade(c.Response(), c.Request(), nil)
	if err != nil {
		slog.Error("ws upgrade failed", "room", code, "err", err)
		return nil
	}

	r := h.manager.GetOrCreate(c.Request().Context(), code, dbRoom.ID)

	// clientID is assigned server-side so it cannot be spoofed by the
	// client. It's used for self-echo prevention: when a broadcast is sent,
	// OriginClientID tells each recipient whether it triggered this event.
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

		// Always override client_id with the server-assigned value.
		ev.ClientID = clientID

		// Persist chat to Postgres asynchronously — room playback events
		// (play/pause/seek) are ephemeral and intentionally not persisted.
		if ev.Type == domain.EventChat && ev.ChatBody != "" {
			go func(body string) {
				ctx := c.Request().Context()
				if _, saveErr := h.chatRepo.Save(ctx, dbRoom.ID, name, body); saveErr != nil {
					slog.Error("failed to persist chat", "room", code, "err", saveErr)
				}
			}(ev.ChatBody)
		}

		// Persist video URL change so new joiners get it from the REST
		// endpoint even after the original setter has left the room.
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

// generateClientID returns a short cryptographically-random hex string.
// Only needs to be unique within a single room session; does not need to
// be a full UUID.
func generateClientID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "fallback-id"
	}
	return fmt.Sprintf("%x", b)
}
