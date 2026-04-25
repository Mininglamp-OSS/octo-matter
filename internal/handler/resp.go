package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func ok(c *gin.Context, data interface{}) {
	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok", "data": data})
}

func created(c *gin.Context, data interface{}) {
	c.JSON(http.StatusCreated, gin.H{"code": 0, "msg": "ok", "data": data})
}

func fail(c *gin.Context, status int, msg string) {
	c.JSON(status, gin.H{
		"error": gin.H{
			"code":    http.StatusText(status),
			"message": msg,
		},
	})
}

func uid(c *gin.Context) string {
	return c.GetString("uid")
}

func spaceID(c *gin.Context) string {
	return c.GetString("space_id")
}
