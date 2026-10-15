local key = KEYS[1]
local seats = cjson.decode(ARGV[1])
local booking_id = ARGV[2]
local booking_data = ARGV[3]

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
for i, pos in ipairs(seats) do
  redis.call('SETBIT', key, pos - 1, 1)
  redis.call('SET', 'owner:'..key..':'..pos, booking_id, 'EX', 604800)
end

-- Step 3: booking mapping (H3 FIX: TTL 7d so release can read layout_version
-- from the lock-time snapshot; the seat bits themselves have no TTL).
redis.call('SET', 'booking:'..booking_id, booking_data, 'EX', 604800)

return cjson.encode({status='SUCCESS', booking_id=booking_id})
