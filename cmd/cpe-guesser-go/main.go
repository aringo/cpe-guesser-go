package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/aringo/cpe-guesser-go/internal/config"
	"github.com/go-redis/redis/v8"
)

var (
	ctx = context.Background()
	rdb *redis.Client
	cfg *config.Config
)

func exactSearch(words []string) ([][2]interface{}, error) {
	keys := make([]string, len(words))
	for i, w := range words {
		keys[i] = "s:" + strings.ToLower(w)
	}
	if len(keys) == 0 {
		return nil, nil
	}
	if len(keys) == 1 {
		return zRevRangeWithScores(keys[0])
	}
	tmp := "tmp:exact"
	if err := rdb.ZInterStore(ctx, tmp, &redis.ZStore{Keys: keys, Aggregate: "SUM"}).Err(); err != nil {
		return nil, err
	}
	defer rdb.Del(ctx, tmp)
	return zRevRangeWithScores(tmp)
}

func partialSearch(words []string) ([][2]interface{}, error) {
	var sets []string
	for _, w := range words {
		iter := rdb.Scan(ctx, 0, "s:*"+strings.ToLower(w)+"*", 0).Iterator()
		for iter.Next(ctx) {
			sets = append(sets, iter.Val())
		}
	}
	if len(sets) == 0 {
		return nil, nil
	}
	tmp := "tmp:partial"
	if err := rdb.ZUnionStore(ctx, tmp, &redis.ZStore{Keys: sets, Aggregate: "SUM"}).Err(); err != nil {
		return nil, err
	}
	defer rdb.Del(ctx, tmp)
	return zRevRangeWithScores(tmp)
}

func zRevRangeWithScores(key string) ([][2]interface{}, error) {
	pairs, err := rdb.ZRevRangeWithScores(ctx, key, 0, -1).Result()
	if err != nil {
		return nil, err
	}
	out := make([][2]interface{}, len(pairs))
	for i, z := range pairs {
		out[i] = [2]interface{}{z.Score, z.Member.(string)}
	}
	return out, nil
}

func handleSearch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Query []string `json:"query"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad JSON", http.StatusBadRequest)
		return
	}

	res, err := exactSearch(req.Query)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if len(res) == 0 {
		res, err = partialSearch(req.Query)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	json.NewEncoder(w).Encode(res)
}

func handleUnique(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Query []string `json:"query"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad JSON", http.StatusBadRequest)
		return
	}

	res, err := exactSearch(req.Query)
	if err == nil && len(res) > 0 {
		json.NewEncoder(w).Encode(res[0][1])
		return
	}
	res, err = partialSearch(req.Query)
	if err == nil && len(res) > 0 {
		json.NewEncoder(w).Encode(res[0][1])
		return
	}
	json.NewEncoder(w).Encode([]string{})
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	// Check Redis connection
	_, err := rdb.Ping(ctx).Result()
	if err != nil {
		http.Error(w, "Redis connection failed", http.StatusServiceUnavailable)
		return
	}

	// Return health status
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status": "healthy",
		"time":   time.Now().Format(time.RFC3339),
	})
}

func runServer() {
	// Define command line flags
	port := flag.String("port", "", "Port to listen on (overrides config)")
	redisHost := flag.String("redis", "", "Redis host:port (overrides config)")
	configPath := flag.String("config", "", "Path to config file (default: search for settings.yaml in current directory)")

	// Parse flags
	flag.Parse()

	// Load config based on flag
	var err error
	cfg, err = config.Load(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Use command line flags if provided, otherwise use config
	serverPort := cfg.Server.Port
	if *port != "" {
		serverPort = 8000 // Default if parsing fails
		if _, err := fmt.Sscanf(*port, "%d", &serverPort); err != nil {
			log.Printf("Invalid port number, using default: %d", serverPort)
		}
	}

	redisAddr := cfg.GetRedisAddr()
	if *redisHost != "" {
		redisAddr = *redisHost
	}

	// Initialize Redis client
	rdb = redis.NewClient(&redis.Options{
		Addr:     redisAddr,
		DB:       8,
		PoolSize: 20,
	})

	// Create server
	mux := http.NewServeMux()
	mux.HandleFunc("/search", handleSearch)
	mux.HandleFunc("/unique", handleUnique)
	mux.HandleFunc("/health", handleHealth)

	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", serverPort),
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	}

	log.Printf("Starting server on port %d", serverPort)
	log.Printf("Redis connection: %s", redisAddr)
	log.Fatal(srv.ListenAndServe())
}

func main() {
	// Check if command is provided
	if len(os.Args) < 2 {
		log.Fatal("Please specify a command: server or import")
	}

	// Get the command and shift arguments
	command := os.Args[1]
	os.Args = append(os.Args[:1], os.Args[2:]...)

	// Reset flag parsing for the subcommand
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	// Execute the appropriate command
	switch command {
	case "server":
		runServer()
	case "import":
		runImport()
	default:
		log.Fatalf("Unknown command: %s", command)
	}
}
