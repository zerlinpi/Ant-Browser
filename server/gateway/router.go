package main

import "github.com/gin-gonic/gin"

func registerRoutes(r *gin.Engine) {
	api := r.Group("/api/v1")

	auth := api.Group("/auth")
	{
		auth.POST("/register", registerHandler)
		auth.POST("/login", loginHandler)
	}
}

func registerHandler(c *gin.Context) {
	c.JSON(200, gin.H{
		"message": "register endpoint ready",
	})
}

func loginHandler(c *gin.Context) {
	c.JSON(200, gin.H{
		"message": "login endpoint ready",
	})
}
