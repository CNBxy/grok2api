package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// CORS 允许浏览器前端（如 infinite-canvas）跨域调用 OpenAI 兼容 API。
// 预检 OPTIONS 在鉴权前直接 204，避免 /v1 路由要求 Bearer 导致预检失败。
func CORS() gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := strings.TrimSpace(c.GetHeader("Origin"))
		if origin == "" {
			origin = "*"
		}
		// 反射 Origin 便于带凭证场景；无 Origin 时退回 *。
		c.Header("Access-Control-Allow-Origin", origin)
		if origin != "*" {
			c.Header("Vary", "Origin")
		}
		c.Header("Access-Control-Allow-Credentials", "true")
		c.Header("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS,HEAD")
		c.Header("Access-Control-Allow-Headers", joinAllowHeaders(c.GetHeader("Access-Control-Request-Headers")))
		c.Header("Access-Control-Expose-Headers", "Content-Type,Content-Length,Content-Disposition,X-Request-Id")
		c.Header("Access-Control-Max-Age", "86400")

		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

func joinAllowHeaders(requested string) string {
	defaults := []string{
		"Authorization",
		"Content-Type",
		"X-API-Key",
		"OpenAI-Organization",
		"OpenAI-Beta",
		"X-Requested-With",
		"Accept",
		"Origin",
		"X-Request-Id",
	}
	seen := make(map[string]struct{}, len(defaults)+8)
	out := make([]string, 0, len(defaults)+8)
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	for _, header := range defaults {
		add(header)
	}
	for _, header := range strings.Split(requested, ",") {
		add(header)
	}
	return strings.Join(out, ", ")
}
