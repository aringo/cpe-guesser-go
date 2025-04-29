package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"sort"
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
	if len(words) == 0 {
		return nil, nil
	}

	// Create keys for each word
	keys := make([]string, len(words))
	for i, w := range words {
		keys[i] = "w:" + strings.ToLower(w)
	}

	// Get intersection of all sets
	var cpes []string
	var err error
	if len(keys) == 1 {
		cpes, err = rdb.SMembers(ctx, keys[0]).Result()
		if err != nil {
			return nil, err
		}
	} else {
		cpes, err = rdb.SInter(ctx, keys...).Result()
		if err != nil {
			return nil, err
		}
	}

	if len(cpes) == 0 {
		return nil, nil
	}

	// Get rank for each CPE
	result := make([][2]interface{}, 0, len(cpes))
	for _, cpe := range cpes {
		rank, err := rdb.ZScore(ctx, "rank:cpe", cpe).Result()
		if err == redis.Nil {
			// If no rank, use 0
			result = append(result, [2]interface{}{0.0, cpe})
		} else if err != nil {
			return nil, err
		} else {
			result = append(result, [2]interface{}{rank, cpe})
		}
	}

	// Sort by rank (highest first)
	sort.Slice(result, func(i, j int) bool {
		return result[i][0].(float64) > result[j][0].(float64)
	})

	return result, nil
}

func partialSearch(words []string) ([][2]interface{}, error) {
	if len(words) == 0 {
		return nil, nil
	}

	// Create a map to store all matching CPEs
	cpeMap := make(map[string]struct{})

	// For each word, find partially matching sets
	for _, w := range words {
		pattern := "w:*" + strings.ToLower(w) + "*"
		iter := rdb.Scan(ctx, 0, pattern, 0).Iterator()

		for iter.Next(ctx) {
			key := iter.Val()
			members, err := rdb.SMembers(ctx, key).Result()
			if err != nil {
				return nil, err
			}

			for _, cpe := range members {
				cpeMap[cpe] = struct{}{}
			}
		}

		if err := iter.Err(); err != nil {
			return nil, err
		}
	}

	if len(cpeMap) == 0 {
		return nil, nil
	}

	// Get rank for each CPE
	result := make([][2]interface{}, 0, len(cpeMap))
	for cpe := range cpeMap {
		rank, err := rdb.ZScore(ctx, "rank:cpe", cpe).Result()
		if err == redis.Nil {
			// If no rank, use 0
			result = append(result, [2]interface{}{0.0, cpe})
		} else if err != nil {
			return nil, err
		} else {
			result = append(result, [2]interface{}{rank, cpe})
		}
	}

	// Sort by rank (highest first)
	sort.Slice(result, func(i, j int) bool {
		return result[i][0].(float64) > result[j][0].(float64)
	})

	return result, nil
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
