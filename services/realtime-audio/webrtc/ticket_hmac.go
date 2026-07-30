package webrtc

import (
	"context"
	"errors"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
)

type HMACTicketValidator struct {
	codec *realtimev1.HMACTicketCodec
}

func NewHMACTicketValidator(codec *realtimev1.HMACTicketCodec) (*HMACTicketValidator, error) {
	if codec == nil {
		return nil, ErrInvalidDependency
	}
	return &HMACTicketValidator{codec: codec}, nil
}

func (v *HMACTicketValidator) Validate(
	ctx context.Context,
	token string,
	sessionID string,
) (ConnectionTicket, error) {
	if err := ctx.Err(); err != nil {
		return ConnectionTicket{}, err
	}
	claims, err := v.codec.Validate(token, sessionID)
	if err != nil {
		return ConnectionTicket{}, mapTicketValidationError(err)
	}
	return ConnectionTicket{
		SessionID: claims.SessionID,
		AccountID: claims.AccountID,
		ExpiresAt: claims.ExpiresAt,
	}, nil
}

func mapTicketValidationError(err error) error {
	switch {
	case errors.Is(err, realtimev1.ErrTicketExpired):
		return ErrTicketExpired
	case errors.Is(err, realtimev1.ErrTicketSessionMismatch):
		return ErrTicketSessionMismatch
	default:
		return ErrRealtimeTokenRequired
	}
}

var _ TicketValidator = (*HMACTicketValidator)(nil)
