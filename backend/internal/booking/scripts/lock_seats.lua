local key = KEYS[1]
local seats = cjson.decode(ARGV[1])
local booking_id = ARGV[2]
local booking_data = ARGV[3]
-- TTL (seconds) for the owner key + booking mapping. Passed from Go
-- (booking.lockKeyTTLSeconds) so the TTL has a single source of truth.
-- Fallback 300s = 180s seat hold + 120s release grace.
local key_ttl = tonumber(ARGV[4]) or 300

-- Step 1: check availability (0-based offset)
for i, pos in ipairs(seats) do
  local bit = redis.call('GETBIT', key, pos - 1)
  if bit == 1 then
    return cjson.encode({status='CONFLICT', position=pos})
  end
end

-- Step 2: lock all + tag per-seat ownership (H2 FIX).
-- owner:{bitmap_key}:{pos} = booking_id allows release to verify it still
-- owns the seat before clearing, preventing double-release from freeing a
-- seat re-locked by another booking.
-- FIX P9a-7 TD-F3-02: TTL was hard-coded to 7 days while the seat is only
-- held for 180s. The owner key now expires with the booking hold + release
-- grace, and MUST keep the same TTL as the booking mapping in step 3
-- (release needs both at the same time).
for i, pos in ipairs(seats) do
  redis.call('SETBIT', key, pos - 1, 1)
  redis.call('SET', 'owner:'..key..':'..pos, booking_id, 'EX', key_ttl)
end

-- Step 3: booking mapping (H3 FIX: keeps the lock-time layout_version
-- snapshot so release targets the right bitmap key; the seat bits
-- themselves have no TTL). FIX P9a-7 TD-F3-02: TTL synced with the owner
-- key in step 2 (was hard-coded to 7 days).
redis.call('SET', 'booking:'..booking_id, booking_data, 'EX', key_ttl)

return cjson.encode({status='SUCCESS', booking_id=booking_id})
