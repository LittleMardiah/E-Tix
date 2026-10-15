package booking

import (
	"context"
	_ "embed"
	"fmt"
	"time"

	"github.com/LittleMardiah/E-Tix/backend/internal/redis"
)

//go:embed scripts/lock_seats.lua
var lockSeatsLua string

//go:embed scripts/release_seats.lua
var releaseSeatsLua string

type LuaScripts struct {
	Lock    string
	Release string
}

func LoadLuaScripts(ctx context.Context, rdb *redis.Client) (*LuaScripts, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// Warm Redis script cache. EVALSHA is not used today (see redis.Client.EvalScript
	// which sends the full body), so the SHA is intentionally not retained.
	if err := rdb.ScriptLoad(ctx, lockSeatsLua).Err(); err != nil {
		return nil, fmt.Errorf("lua_scripts: load lock: %w", err)
	}
	if err := rdb.ScriptLoad(ctx, releaseSeatsLua).Err(); err != nil {
		return nil, fmt.Errorf("lua_scripts: load release: %w", err)
	}
	return &LuaScripts{
		Lock:    lockSeatsLua,
		Release: releaseSeatsLua,
	}, nil
}
