package commentator

import "errors"

var (
	ErrInvalidToken    = errors.New("invalid session token")
	ErrExpiredToken    = errors.New("session token expired")
	ErrSessionDisabled = errors.New("commentator session disabled")
	ErrPinRequired     = errors.New("pin required")
	ErrInvalidPin      = errors.New("invalid pin")
	ErrInvalidDeckCode = errors.New("invalid pairing code")
)
