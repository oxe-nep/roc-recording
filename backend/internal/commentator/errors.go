package commentator

import "errors"

var (
	errInvalidToken    = errors.New("invalid session token")
	errExpiredToken    = errors.New("session token expired")
	errSessionDisabled = errors.New("commentator session disabled")
)
