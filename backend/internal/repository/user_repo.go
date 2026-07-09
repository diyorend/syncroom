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

type UserRepo struct {
	db *pgxpool.Pool
}

func NewUserRepo(db *pgxpool.Pool) *UserRepo {
	return &UserRepo{db: db}
}

var _ UserStore = (*UserRepo)(nil)

func (r *UserRepo) Create(ctx context.Context, email, hashedPassword string) (*domain.User, error) {
	var u domain.User
	err := r.db.QueryRow(ctx,
		`INSERT INTO users (email, password)
		 VALUES ($1, $2)
		 RETURNING id, email, password, created_at`,
		email, hashedPassword,
	).Scan(&u.ID, &u.Email, &u.Password, &u.CreatedAt)

	if err != nil {
		if strings.Contains(err.Error(), "23505") {
			return nil, fmt.Errorf("email %s: %w", email, domain.ErrAlreadyExists)
		}
		return nil, fmt.Errorf("UserRepo.Create: %w", err)
	}
	return &u, nil
}

func (r *UserRepo) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	var u domain.User
	err := r.db.QueryRow(ctx,
		`SELECT id, email, password, created_at FROM users WHERE email = $1`,
		email,
	).Scan(&u.ID, &u.Email, &u.Password, &u.CreatedAt)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("user %s: %w", email, domain.ErrNotFound)
		}
		return nil, fmt.Errorf("UserRepo.GetByEmail: %w", err)
	}
	return &u, nil
}
