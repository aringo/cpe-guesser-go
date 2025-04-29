# CPE Guesser (Go)

CPE Guesser is a high-performance Go implementation of the CPE guessing service. It provides a web API to guess CPE names based on one or more keywords. The resulting CPE can be used with tools like [cve-search](https://github.com/cve-search/cve-search) or [vulnerability-lookup](https://github.com/cve-search/vulnerability-lookup) to perform actual searches using CPE names.

This is a Go port of the original [CPE Guesser](https://github.com/cve-search/cpe-guesser) project.

## Requirements

- [Valkey](https://valkey.io/)
- Go 1.21 or later
- Make (optional, for build automation)
- Docker and Docker Compose (for running Valkey)

## Installation

### From Binary Release

Download the latest release for your platform from the [Releases](https://github.com/aringo/cpe-guesser-go/releases) page.

```bash
# Example for Linux x86_64
wget https://github.com/aringo/cpe-guesser-go/releases/latest/download/cpe-guesser-go_Linux_x86_64.tar.gz
tar xf cpe-guesser-go_Linux_x86_64.tar.gz
sudo mv cpe-guesser-go /usr/local/bin/
```

### From Source

```bash
# Clone the repository
git clone git@github.com:aringo/cpe-guesser-go.git
cd cpe-guesser-go

# Build using make (recommended)
make build

# Install (optional)
make install
```

Or manually:
```bash
# Initialize and download dependencies
go mod download
go mod tidy

# Build the binary
go build -o cpe-guesser-go ./cmd/cpe-guesser-go

# Install (optional)
sudo mv cpe-guesser-go /usr/local/bin/
```

### Using Go Install

```bash
# Install directly from GitHub
go install github.com/aringo/cpe-guesser-go/cmd/cpe-guesser-go@latest
```

After installation, the binary will be available as `cpe-guesser-go` in your `$GOPATH/bin` directory.

## Configuration

The application uses a YAML configuration file (`settings.yaml`) with the following structure:

```yaml
server:
  port: 8000
valkey:
  host: 127.0.0.1
  port: 6379
cpe:
  path: '../data/official-cpe-dictionary_v2.3.xml'
  source: 'https://nvd.nist.gov/feeds/xml/cpe/dictionary/official-cpe-dictionary_v2.3.xml.gz'
```

The configuration file is searched for in the current directory by default. You can specify a custom config file path using the `-config` flag:

```bash
cpe-guesser-go -config /path/to/settings.yaml server
```

## Usage

The application provides two main commands: `server` and `import`.

### Import Command

The import command populates the Valkey database with CPE data:

```bash
# If installed via make or manual build
./cpe-guesser-go import

# If installed via go install
cpe-guesser-go import
```

Import options:
- `-download`: Download CPE data even if file exists
- `-replace`: Flush and repopulate the CPE database
- `-update`: Update the CPE database without flushing
- `-redis`: Redis host:port (overrides config)
- `-config`: Path to config file (default: search for settings.yaml in current directory)

### Server Command

The server command starts the web API:

```bash
# If installed via make or manual build
./cpe-guesser-go server

# If installed via go install
cpe-guesser-go server
```

Or with custom options:

```bash
# If installed via make or manual build
./cpe-guesser-go server -port 8080 -redis localhost:6379

# If installed via go install
cpe-guesser-go server -port 8080 -redis localhost:6379
```

Server options:
- `-port`: Port to listen on (overrides config)
- `-redis`: Redis host:port (overrides config)
- `-config`: Path to config file (default: search for settings.yaml in current directory)

## API Endpoints

### Search Endpoint

```bash
curl -s -X POST http://localhost:8000/search -d '{"query": ["tomcat"]}' | jq .
```

Response:
```json
[
  [
    18117,
    "cpe:2.3:a:apache:tomcat"
  ],
  [
    60947,
    "cpe:2.3:a:oracle:tomcat"
  ]
]
```

### Unique Endpoint

```bash
curl -s -X POST http://localhost:8000/unique -d '{"query": ["tomcat"]}' | jq .
```

Response:
```json
"cpe:2.3:a:apache:tomcat"
```

### Health Endpoint

```bash
curl -s http://localhost:8000/health | jq .
```

Response:
```json
{
  "status": "healthy",
  "time": "2024-03-21T10:00:00Z"
}
```

## Docker Setup

The Docker setup is designed to run only the Valkey database, while the Go binary runs directly on the host for better performance. The database is only accessible from localhost for security.

### Running with Docker

1. Start the Valkey database:
```bash
docker compose -f docker-compose.yml up -d
```

2. Run the Go binary:
```bash
# Import data
cpe-guesser-go import

# Start server
cpe-guesser-go server
```

## How it Works

The Go implementation maintains the same core functionality as the Python version:

1. Splits vendor and product names into individual words
2. Creates an inverse index using the CPE vendor:product format as value
3. Builds ranked sets with the most common CPEs per version
4. Provides probability-based matching through exact and partial search

## License

Software is open source and released under a 2-Clause BSD License

```
Copyright (c) 2021-2024 Alexandre Dulaunoy
Copyright (c) 2021-2024 Esa Jokinen
Copyright (c) 2025 Aaron Ringo
```

By contributing, contributors also acknowledge the [Developer Certificate of Origin](https://developercertificate.org/) when submitting pull requests or using other methods of contribution. 

# .goreleaser.yml
before:
  hooks:
    - go mod tidy

builds:
  - env:
      - CGO_ENABLED=0
    goos:
      - linux
      - windows
      - darwin
    goarch:
      - amd64
      - arm64
    ignore:
      - goos: windows
        goarch: arm64
    binary: cpe-guesser-go

archives:
  - format: tar.gz
    name_template: >-
      {{ .ProjectName }}_
      {{- title .Os }}_
      {{- if eq .Arch "amd64" }}x86_64
      {{- else if eq .Arch "386" }}i386
      {{- else }}{{ .Arch }}{{ end }}
    format_overrides:
      - goos: windows
        format: zip
    files:
      - README.md
      - LICENSE
      - settings.yaml

changelog:
  sort: asc
  filters:
    exclude:
      - '^docs:'
      - '^test:'
      - '^ci:'
      - Merge pull request
      - Merge branch

snapshot:
  name_template: "{{ incpatch .Version }}-next"

release:
  github:
    owner: aringo
    name: cpe-guesser-go
  prerelease: auto
  draft: false 