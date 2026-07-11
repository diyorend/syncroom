package service

import (
	"context"
	"fmt"
	"time"

	"github.com/diyorend/syncroom/internal/domain"
	"github.com/diyorend/syncroom/internal/repository"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

type Claims struct {
	UserID string `json:"user_id"`
	Email  string `json:"email"`
	jwt.RegisteredClaims
}

type AuthService struct {
	userStore repository.UserStore
	jwtSecret string
	expiry    time.Duration
}

func NewAuthService(store repository.UserStore, secret string, expiryHours int) *AuthService {
	return &AuthService{
		userStore: store,
		jwtSecret: secret,
		expiry:    time.Duration(expiryHours) * time.Hour,
	}
}

func (s *AuthService) Register(ctx context.Context, email, password string) (string, *domain.User, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", nil, fmt.Errorf("AuthService.Register: %w", err)
	}
	user, err := s.userStore.Create(ctx, email, string(hash))
	if err != nil {
		return "", nil, err
	}
	token, err := s.generateToken(user)
	if err != nil {
		return "", nil, err
	}
	return token, user, nil
}

func (s *AuthService) Login(ctx context.Context, email, password string) (string, *domain.User, error) {
	user, err := s.userStore.GetByEmail(ctx, email)
	if err != nil {
		return "", nil, fmt.Errorf("%w", domain.ErrUnauthorized)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(password)); err != nil {
		return "", nil, fmt.Errorf("%w", domain.ErrUnauthorized)
	}
	token, err := s.generateToken(user)
	if err != nil {
		return "", nil, err
	}
	return token, user, nil
}

func (s *AuthService) ValidateToken(tokenStr string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(s.jwtSecret), nil
	})
	if err != nil || !token.Valid {
		return nil, fmt.Errorf("%w", domain.ErrUnauthorized)
	}
	claims, ok := token.Claims.(*Claims)
	if !ok {
		return nil, fmt.Errorf("%w", domain.ErrUnauthorized)
	}
	return claims, nil
}

func (s *AuthService) generateToken(user *domain.User) (string, error) {
	claims := Claims{
		UserID: user.ID,
		Email:  user.Email,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(s.expiry)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(s.jwtSecret))
}
