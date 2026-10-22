# ID Generator (`gokit/idgen`)

`idgen` provides high-throughput, unique distributed identifier generators including UUID v4, monotonic time-ordered UUID v7, ULID, and URL-friendly NanoID.

---

## Formats & Characteristics

| Generator | Length | Time-Ordered? | Description |
|---|---|---|---|
| `idgen.NewUUID()` | 36 chars | No | Standard RFC 4122 random UUID v4 |
| `idgen.NewUUIDv7()` | 36 chars | **Yes** (millisecond) | Optimal for database primary keys (eliminates B-Tree index fragmentation) |
| `idgen.NewULID()` | 26 chars | **Yes** (millisecond) | Crockford Base32 encoded, lexicographically sortable |
| `idgen.NewNanoID()` | 21 chars | No | Compact, URL-friendly unique identifier |
| `idgen.NewShardIDGenerator(shardID)` | 64-bit int | **Yes** (millisecond) | Snowflake/Instagram-style 64-bit ID with embedded 13-bit logical shard routing |

---

## Quick Start

```go
package main

import (
    "fmt"

    "github.com/dhawalhost/gokit/idgen"
)

func main() {
    uuid4 := idgen.NewUUID()     // "f47ac10b-58cc-4372-a567-0e02b2c3d479"
    uuid7 := idgen.NewUUIDv7()   // "018f4a1b-3b32-7f28-b80c-98782a4d0482" (Time-ordered)
    ulid  := idgen.NewULID()     // "01ARZ3NDEKTSV4RRFFQ69G5FAV"
    nano  := idgen.MustNanoID()  // "V1StGXR8_Z5jdHi6B-myT"

    // 64-bit Shard-Aware ID (Instagram / Notion architecture)
    shardGen, _ := idgen.NewShardIDGenerator(480) // Logical shard 480
    shardID, _ := shardGen.NextID()               // e.g. 142278492049281024

    // O(1) bitshift routing without database lookups:
    route := idgen.ExtractShardID(shardID)       // returns 480

    fmt.Println(uuid4, uuid7, ulid, nano, shardID, route)
}
```

