package timeout

import "time"

// Operation bounds one daemon RPC's CDP work.
const Operation = 60 * time.Second
