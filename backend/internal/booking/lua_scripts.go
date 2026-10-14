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
	Lock       string
	Release    string
	LockSHA    string
	ReleaseSHA string
}

func LoadLuaScripts(ctx context.Context, rdb *redis.Client) (*LuaScripts, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	lockSHA, err := rdb.ScriptLoad(ctx, lockSeatsLua).Result()
	if err != nil {
		return nil, fmt.Errorf("lua_scripts: load lock: %w", err)
	}
	releaseSHA, err := rdb.ScriptLoad(ctx, releaseSeatsLua).Result()
	if err != nil {
		return nil, fmt.Errorf("lua_scripts: load release: %w", err)
	}
	return &LuaScripts{
		Lock:       lockSeatsLua,
		Release:    releaseSeatsLua,
		LockSHA:    lockSHA,
		ReleaseSHA: releaseSHA,
	}, nil
}
