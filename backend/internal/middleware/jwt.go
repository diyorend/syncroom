package middleware

import (
	"net/http"
	"strings"

	"github.com/diyorend/syncroom/internal/service"
	"github.com/labstack/echo/v4"
)

const claimsKey = "user_claims"

// JWT validates the token from the Authorization header (REST calls) or
// the ?token= query param (WebSocket upgrade requests, where the browser's
// WebSocket API cannot set custom headers).
func JWT(authSvc *service.AuthService) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			tokenStr := extractToken(c)
			if tokenStr == "" {
				return c.JSON(http.StatusUnauthorized, map[string]string{"error": "missing token"})
			}
			claims, err := authSvc.ValidateToken(tokenStr)
			if err != nil {
				return c.JSON(http.StatusUnauthorized, map[string]string{"error": "invalid token"})
			}
			c.Set(claimsKey, claims)
			return next(c)
		}
	}
}

func extractToken(c echo.Context) string {
	if h := c.Request().Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	return c.QueryParam("token")
}

func GetClaims(c echo.Context) *service.Claims {
	v := c.Get(claimsKey)
	if v == nil {
		return nil
	}
	claims, _ := v.(*service.Claims)
	return claims
}

func RequireAuth(c echo.Context) (string, error) {
	claims := GetClaims(c)
	if claims == nil {
		return "", c.JSON(http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
	}
	return claims.UserID, nil
}

// OptionalJWT is like JWT but does not reject requests with no token.
// If a valid token is present it sets the claims; if not, the handler
// proceeds with no claims (GetClaims returns nil). Used for the WebSocket
// endpoint where guests can join without an account.
func OptionalJWT(authSvc *service.AuthService) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if tokenStr := extractToken(c); tokenStr != "" {
				if claims, err := authSvc.ValidateToken(tokenStr); err == nil {
					c.Set(claimsKey, claims)
				}
			}
			return next(c)
		}
	}
}
