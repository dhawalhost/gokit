module github.com/dhawalhost/gokit/x

go 1.26.11

require (
	github.com/alicebob/miniredis/v2 v2.37.0
	github.com/andybalholm/brotli v1.2.7
	github.com/dhawalhost/gokit v0.11.0
	github.com/google/uuid v1.6.0
	github.com/klauspost/compress v1.20.4
	github.com/redis/go-redis/v9 v9.18.0
	go.uber.org/zap v1.27.1
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/matoous/go-nanoid/v2 v2.1.0 // indirect
	github.com/oklog/ulid/v2 v2.1.1 // indirect
	github.com/yuin/gopher-lua v1.1.1 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	go.uber.org/multierr v1.10.0 // indirect
)

replace github.com/dhawalhost/gokit => ..
