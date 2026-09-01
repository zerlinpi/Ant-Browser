package jwt

// TokenService will provide JWT generation and validation.
// Implementation will be connected with user-service authentication.
type TokenService struct {
	Secret string
}

func NewTokenService(secret string) *TokenService {
	return &TokenService{Secret: secret}
}
