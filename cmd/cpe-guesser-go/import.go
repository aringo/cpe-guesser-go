package main

import (
	"compress/gzip"
	"context"
	"encoding/xml"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/aringo/cpe-guesser-go/internal/config"
	"github.com/go-redis/redis/v8"
)

// XMLEntry maps only the cpe23-item element's name attribute
type XMLEntry struct {
	Name string `xml:"name,attr"`
}

const (
	batchSize = 5000
)

func runImport() {
	// Define command line flags
	down := flag.Bool("download", false, "Download CPE data even if file exists")
	replace := flag.Bool("replace", false, "Flush and repopulate the CPE database")
	update := flag.Bool("update", false, "Update the CPE database without flushing")
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
	redisAddr := cfg.GetRedisAddr()
	if *redisHost != "" {
		redisAddr = *redisHost
	}

	// Initialize Redis client
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{
		Addr:     redisAddr,
		DB:       8,
		PoolSize: 20,
	})

	// Verify Redis connection
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatalf("Failed to connect to Redis: %v", err)
	}

	// Check existing keys
	dbSize, err := rdb.DBSize(ctx).Result()
	if err != nil {
		log.Fatalf("Redis DBSize error: %v", err)
	}
	if dbSize > 0 && !*replace && !*update {
		log.Fatalf("Warning: Redis contains %d keys. Use --replace or --update.", dbSize)
	}

	// Download if requested or missing
	cpePath := cfg.GetCPEPath()
	if *down || !fileExists(cpePath) {
		fmt.Printf("Downloading CPE data from %s ...\n", cfg.CPE.Source)
		eresp, err := http.Get(cfg.CPE.Source)
		if err != nil {
			log.Fatalf("HTTP error: %v", err)
		}
		defer eresp.Body.Close()

		// stream to .gz file
		gzPath := cpePath + ".gz"
		out, err := os.Create(gzPath)
		if err != nil {
			log.Fatalf("File create error: %v", err)
		}
		if _, err := io.Copy(out, eresp.Body); err != nil {
			out.Close()
			log.Fatalf("Failed to download file: %v", err)
		}
		out.Close()

		// decompress
		fmt.Printf("Uncompressing %s ...\n", gzPath)
		if err := gunzip(gzPath, cpePath); err != nil {
			log.Fatalf("gunzip error: %v", err)
		}
		os.Remove(gzPath)
	} else {
		fmt.Printf("Using existing file %s\n", cpePath)
	}

	// Flush if replace
	if dbSize > 0 && *replace {
		fmt.Printf("Flushing %d keys...\n", dbSize)
		if err := rdb.FlushDB(ctx).Err(); err != nil {
			log.Fatalf("Failed to flush database: %v", err)
		}
	}

	// Parse and populate
	fmt.Println("Populating the database (this may take a while)...")
	f, err := os.Open(cpePath)
	if err != nil {
		log.Fatalf("Open CPE file: %v", err)
	}
	defer f.Close()

	decoder := xml.NewDecoder(f)
	itemCount := 0
	wordCount := 0
	start := time.Now()
	pipe := rdb.Pipeline()

	for {
		tok, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Fatalf("XML parse error: %v", err)
		}

		switch se := tok.(type) {
		case xml.StartElement:
			if se.Name.Local == "cpe23-item" {
				var xe XMLEntry
				if err := decoder.DecodeElement(&xe, &se); err != nil {
					log.Fatalf("XML decode error: %v", err)
				}
				vendor, product, cpeline := extract(xe.Name)

				// index words - use SAdd for intersection (like Python)
				for _, w := range canonize(vendor) {
					pipe.SAdd(ctx, "w:"+w, cpeline)                             // Set membership for intersection
					pipe.ZIncrBy(ctx, "s:"+w, 1, cpeline) // Keep for compatibility
					wordCount++
				}
				for _, w := range canonize(product) {
					pipe.SAdd(ctx, "w:"+w, cpeline)                             // Set membership for intersection
					pipe.ZIncrBy(ctx, "s:"+w, 1, cpeline) // Keep for compatibility
					wordCount++
				}

				// Increment counter first to start with 1
				itemCount++

				// Add to rank:cpe with increasing rank (higher rank = better match)
				pipe.ZIncrBy(ctx, "rank:cpe", 1, cpeline)

				if itemCount%batchSize == 0 {
					if _, err := pipe.Exec(ctx); err != nil {
						log.Fatalf("Pipeline execution error: %v", err)
					}
					pipe = rdb.Pipeline() // Create new pipeline
					fmt.Printf("... %d items (%d words) in %s\n", itemCount, wordCount, time.Since(start))
				}
			}
		}
	}

	// flush final pipeline
	if _, err := pipe.Exec(ctx); err != nil {
		log.Fatalf("Final pipeline execution error: %v", err)
	}

	elapsed := time.Since(start)
	finalSize, err := rdb.DBSize(ctx).Result()
	if err != nil {
		log.Printf("Warning: Could not get final DB size: %v", err)
		finalSize = 0
	}
	fmt.Printf("Done! %d items, %d words in %s. DB size: %d\n", itemCount, wordCount, elapsed, finalSize)
}

func extract(cpe string) (vendor, product, cpeline string) {
	parts := strings.Split(cpe, ":")
	if len(parts) < 5 {
		return "", "", cpe
	}
	vendor = parts[3]
	product = parts[4]
	cpeline = strings.Join(parts[:5], ":")
	return vendor, product, cpeline
}

func canonize(val string) []string {
	val = strings.ToLower(val)
	return strings.Split(val, "_")
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func gunzip(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	gr, err := gzip.NewReader(in)
	if err != nil {
		return err
	}
	defer gr.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, gr)
	return err
}
