local key = KEYS[1]
local seats = cjson.decode(ARGV[1])
local booking_id = ARGV[2]
local released = 0
local skipped_owner_mismatch = 0

-- H2 FIX: only clear a bit if this booking still owns it. If another
-- booking re-locked the seat after our mapping expired (or after a prior
-- release raced with a fresh lock), we must NOT clear that bit.
for i, pos in ipairs(seats) do
  local owner_key = 'owner:'..key..':'..pos
  local owner = redis.call('GET', owner_key)
  if owner == booking_id then
    local bit = redis.call('GETBIT', key, pos - 1)
    if bit == 1 then
      redis.call('SETBIT', key, pos - 1, 0)
      released = released + 1
    end
    redis.call('DEL', owner_key)
  else
    skipped_owner_mismatch = skipped_owner_mismatch + 1
  end
end

redis.call('DEL', 'booking:'..booking_id)
return cjson.encode({status='SUCCESS', released=released, skipped=skipped_owner_mismatch})
