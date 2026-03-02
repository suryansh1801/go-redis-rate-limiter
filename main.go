package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

var rdb *redis.Client

func initRedis() {
	rdb = redis.NewClient(&redis.Options{
		Addr:     "localhost:6379",
		Password: "",
		DB:       0,
	})

	// Call Redis to ensure it's alive before starting the server
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatalf("❌ Failed to connect to Redis: %v", err)
	}

	fmt.Printf("🛢️  Successfully connected to Redis!")
}

func RateLimiter(limit int, window time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		clientIP := c.ClientIP()

		// creating a unique redis key for this user
		key := fmt.Sprintf("rate_limit: %s ", clientIP)

		// Using request's context to talk to Redis
		ctx := c.Request.Context()

		// INC increment the counter in redis
		count, err := rdb.Incr(ctx, key).Result()
		if err != nil {
			log.Println("Redis error:", err)
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "Internal Server Error"})
			return
		}

		// If this is their very first request, set the expiration timer (the "Window")
		if count == 1 {
			rdb.Expire(ctx, key, window)
		}

		if count > int64(limit) {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error":       "🛑 Rate limit exceeded.",
				"retry_after": window.String(),
			})
			return
		}

		c.Header("X-RateLimit-Limit", fmt.Sprintf("%d", limit))
		c.Header("X-RateLimit-Remaining", fmt.Sprintf("%d", int64(limit)-count))

		// Pass control to the actual route handler
		c.Next()
	}
}

func main() {
	initRedis()
	r := gin.Default()
	r.Use(RateLimiter(5, 1*time.Minute))

	r.GET("/api/data", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"message": "✅ Success! Your request has been processed.",
		})
	})

	fmt.Println("🚀 Server running on http://localhost:8080")
	r.Run(":8080")
}
