package middleware

import "github.com/gin-gonic/gin"

// JWTAuth validates user access tokens.
// Future implementation will connect shared/jwt service.
func JWTAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		// TODO:
		// 1. Read Authorization header
		// 2. Validate JWT
		// 3. Inject user identity
		c.Next()
	}
}
