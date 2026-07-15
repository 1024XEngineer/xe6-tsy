package main

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func serverAddress(port string) string {
	if port == "" {
		port = "8080"
	}
	return ":" + port
}

func run(addr string) error {
	return http.ListenAndServe(addr, newRouter(nil))
}

func newRouter(register func(*gin.Engine)) *gin.Engine {
	router := gin.New()
	router.Use(gin.Recovery())
	router.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	if register != nil {
		register(router)
	}
	return router
}
