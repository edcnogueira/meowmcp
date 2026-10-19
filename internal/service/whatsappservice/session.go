package whatsappservice

import (
	"context"
	"errors"

	"github.com/edcnogueira/meowmcp/internal/domain"
)

func (s *Service) Status() domain.Status {
	return s.whatsapp.Status()
}

type LoginRequest struct {
	Phone string
}

func (s *Service) Login(ctx context.Context, req *LoginRequest) (domain.Status, error) {
	st, err := s.whatsapp.Login(ctx, req.Phone)
	if errors.Is(err, domain.ErrAlreadyLoggedIn) {
		return st, nil
	}
	return st, err
}

func (s *Service) Logout(ctx context.Context) error {
	return s.whatsapp.Logout(ctx)
}
