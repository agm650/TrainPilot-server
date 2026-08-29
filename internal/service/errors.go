package service

import "errors"

var (
	ErrPermissionDenied    = errors.New("permission denied")
	ErrValidation          = errors.New("validation failed")
	ErrLeaseNotOwned       = errors.New("lease is owned by another session")
	ErrAccessTokenExpired  = errors.New("access token expired")
	ErrRefreshTokenExpired = errors.New("refresh token expired")
	ErrInvalidAccessToken  = errors.New("invalid access token")
	ErrInvalidRefreshToken = errors.New("invalid refresh token")
)

type validationError struct {
	message string
}

func (e validationError) Error() string { return e.message }
func (e validationError) Is(target error) bool {
	return target == ErrValidation
}

func invalid(message string) error {
	return validationError{message: message}
}
