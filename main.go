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

func FixedWindowCounter(limit int, window time.Duration) gin.HandlerFunc {
	rateLimitScript := redis.NewScript(`
		local current = redis.call("INCR", KEYS[1])
		if current == 1 then
			redis.call("EXPIRE", KEYS[1], ARGV[1])
		end
		return current
	`)

	return func(c *gin.Context) {
		clientIP := c.ClientIP()

		// creating a unique redis key for this user
		key := fmt.Sprintf("rate_limit: %s ", clientIP)

		// Using request's context to talk to Redis
		ctx := c.Request.Context()

		// INC increment the counter in redis through Lua script
		result, err := rateLimitScript.Run(ctx, rdb, []string{key}, int(window.Seconds())).Result()

		if err != nil {
			log.Println("Redis Lua script error:", err)
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "Internal Server Error"})
			return
		}

		count := result.(int64)

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

func SlidingWindowLimiter(limit int, window time.Duration) gin.HandlerFunc {
	slidingWindowScript := redis.NewScript(`
		local key = KEYS[1]
		local now = tonumber(ARGV[1])
		local cutoff = tonumber(ARGV[2])
		local limit = tonumber(ARGV[3])

		redis.call('ZREMRANGEBYSCORE', key, 0, cutoff)

		local current_requests = redis.call('ZCARD', key)

		if current_requests >= limit then
			return 0
		end

		redis.call('ZADD', key, now, now)
		local ttl = math.ceil((now - cutoff) / 1000)
		redis.call('EXPIRE', key, ttl)

		return 1
	`)

	return func(c *gin.Context) {
		clientIP := c.ClientIP()
		key := fmt.Sprintf("sliding_limit:%s", clientIP)
		ctx := c.Request.Context()

		// Calculate the timestamps in Milliseconds
		now := time.Now().UnixMilli()
		cutoff := now - window.Milliseconds()

		// Execute the script
		// KEYS: [key]
		// ARGV: [now, cutoff, limit]
		result, err := slidingWindowScript.Run(ctx, rdb, []string{key}, now, cutoff, limit).Result()

		if err != nil {
			log.Println("Redis Lua script error:", err)
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "Internal Server Error"})
			return
		}

		// The script returns 1 for success, 0 for rejected
		allowed := result.(int64)

		if allowed == 0 {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": "🛑 Sliding window limit exceeded! Please slow down.",
			})
			return
		}

		c.Next()
	}
}

func main() {
	initRedis()
	r := gin.Default()
	r.Use(SlidingWindowLimiter(5, 1*time.Second))

	r.GET("/api/data", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"message": "✅ Success! Your request has been processed.",
		})
	})

	fmt.Println("🚀 Server running on http://localhost:8080")
	r.Run(":8080")
}
