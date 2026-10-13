package auth

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrUserNotFound = errors.New("auth: user not found")
	ErrEmailExists  = errors.New("auth: email already exists")
	ErrPhoneExists  = errors.New("auth: phone already exists")
)

type UserRepository interface {
	CreateUser(ctx context.Context, user *User) error
	GetUserByEmail(ctx context.Context, email string) (*User, error)
	GetUserByID(ctx context.Context, id uuid.UUID) (*User, error)
}

type PostgresUserRepository struct {
	db *pgxpool.Pool
}

func NewPostgresUserRepository(db *pgxpool.Pool) *PostgresUserRepository {
	return &PostgresUserRepository{db: db}
}

const userColumns = `id, email, phone, name, password_hash, role, status, ` +
	`base_score, total_purchases, first_purchase_at, last_purchase_at, ` +
	`profile_photo_url, created_at, updated_at`

func scanUser(row pgx.Row) (*User, error) {
	var u User
	if err := row.Scan(
		&u.ID,
		&u.Email,
		&u.Phone,
		&u.Name,
		&u.PasswordHash,
		&u.Role,
		&u.Status,
		&u.BaseScore,
		&u.TotalPurchases,
		&u.FirstPurchaseAt,
		&u.LastPurchaseAt,
		&u.ProfilePhotoURL,
		&u.CreatedAt,
		&u.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return &u, nil
}

func mapUserWriteError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		switch pgErr.ConstraintName {
		case "users_email_key":
			return ErrEmailExists
		case "users_phone_key":
			return ErrPhoneExists
		}
	}
	return err
}

func (r *PostgresUserRepository) CreateUser(ctx context.Context, user *User) error {
	const query = `INSERT INTO users (email, phone, name, password_hash, role, status)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, created_at, updated_at`

	err := r.db.QueryRow(ctx, query,
		user.Email,
		user.Phone,
		user.Name,
		user.PasswordHash,
		user.Role,
		user.Status,
	).Scan(&user.ID, &user.CreatedAt, &user.UpdatedAt)
	if err != nil {
		return mapUserWriteError(err)
	}
	return nil
}

func (r *PostgresUserRepository) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	const query = `SELECT ` + userColumns + ` FROM users WHERE email = $1 LIMIT 1`

	user, err := scanUser(r.db.QueryRow(ctx, query, email))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	return user, nil
}

func (r *PostgresUserRepository) GetUserByID(ctx context.Context, id uuid.UUID) (*User, error) {
	const query = `SELECT ` + userColumns + ` FROM users WHERE id = $1`

	user, err := scanUser(r.db.QueryRow(ctx, query, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	return user, nil
}
