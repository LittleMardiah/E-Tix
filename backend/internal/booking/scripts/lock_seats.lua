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

-- Step 2: lock all
for i, pos in ipairs(seats) do
  redis.call('SETBIT', key, pos - 1, 1)
end

-- Step 3: booking mapping TTL 180s
redis.call('SET', 'booking:'..booking_id, booking_data, 'EX', 180)

return cjson.encode({status='SUCCESS', booking_id=booking_id})
