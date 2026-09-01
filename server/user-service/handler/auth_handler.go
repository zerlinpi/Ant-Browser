package handler

// AuthHandler exposes register and login endpoints.
// It will connect HTTP requests with user-service.
type AuthHandler struct {
}

func NewAuthHandler() *AuthHandler {
	return &AuthHandler{}
}

// Register creates a new user.
func (h *AuthHandler) Register() {
	// TODO: validate request, hash password, save user
}

// Login authenticates user and returns JWT.
func (h *AuthHandler) Login() {
	// TODO: verify password and generate token
}
